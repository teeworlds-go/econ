package econ

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/reiver/go-telnet"
)

var (
	// ErrAuthenticationFailed is returned when the external console authentication failed
	ErrAuthenticationFailed = errors.New("authentication failed")

	// PasswordLine is the line that is expected when the external console requests a password
	PasswordLine = "Enter password:"

	// AuthenticationSuccessLine is the line that is expected when the external console grants access
	AuthenticationSuccessLine = "Authentication successful. External console access granted."

	// LogoutCommand is the command that is used to logout from the external console
	LogoutCommand = "logout"
)

// DialTo creates a new econ connection that can be used to write or read lines from
// the teeworlds server via the external console. (The New function is a wrapper around DialTo)
// address is the <IP>:<PORT(ec_port)> address
// the password is the one you set via: ec_password
// You may want to decrease the ec_auth_timeout in order to get disconnected faster and not to block
// any of the 4 existing econ slots.
// You can also set your ec_bantime to anything other than 0 in order to ban people that try to connect to you external console and try incorrect credentials
// ec_output_level [1,2] allows to increase the logging level of your external console. This allows for more verbose econ output parsing
func DialTo(address, password string, options ...Option) (conn *Conn, err error) {

	c := &Conn{
		ctx:               context.Background(),
		telnetConn:        nil,
		address:           address,
		password:          password,
		maxReconnectDelay: 10 * time.Second,
	}

	defer func() {
		if err != nil {
			_ = c.Close()
		}
	}()

	for _, option := range options {
		option(c)
	}
	c.ctx, c.cancel = context.WithCancel(c.ctx)
	c.backoff = newBackoffPolicy(max(50*time.Millisecond, c.maxReconnectDelay/20), c.maxReconnectDelay)

	err = c.reconnect()
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Conn is the telnet connection to a teeworlds external console terminal(econ)
type Conn struct {
	ctx               context.Context
	cancel            context.CancelFunc
	telnetConn        *telnet.Conn
	address           string
	password          string
	maxReconnectDelay time.Duration
	authCommandList   []string
	backoff           backoffFunc
}

// Close must be called when the connection is to be quit
func (c *Conn) Close() (err error) {
	c.cancel()
	if c.telnetConn != nil {
		_ = c.logout()
		err = c.telnetConn.Close()
		c.telnetConn = nil
	}
	return err
}

func (c *Conn) logout() error {
	return c.unguardedWriteLine(LogoutCommand)
}

// ReadLine reads a line from the external console
// if the connection is lost, it attempts to reconnect multiple times before
// trying to read the line again.
func (c *Conn) ReadLine() (line string, err error) {
	err = c.guard(func() (err error) {
		line, err = c.unguardedReadLine()
		return err
	})
	return line, err
}

func (c *Conn) guard(f func() error) error {

	// try without retry overhead
	err := f()
	if err == nil {
		return nil
	}

	// add retry overhead
	return c.retry(func() (retry bool, err error) {
		err = c.reconnect()
		if err != nil {
			return false, err
		}
		err = f()
		if err == nil {
			return false, nil
		}
		return true, err
	})
}

// no reconnect mechanisms guard this line reading
func (c *Conn) unguardedReadLine() (string, error) {
	if c.telnetConn == nil {
		return "", errors.New("telnet connection is nil")
	}

	// a line may at most have 256 characters
	stackArray := [256]byte{}
	stackArraySlice := stackArray[:0]
	lineBuffer := bytes.NewBuffer(stackArraySlice)

	singleCharBuffer := [1]byte{}
	singleCharBufferSlice := singleCharBuffer[:]

	// we read single byte arrays until we hit a linebreak
	for {
		n, err := c.telnetConn.Read(singleCharBufferSlice)
		if err != nil {
			return "", err
		}
		// failed to read one byte
		if n == 0 {
			continue
		}
		// we do hit a linebreak
		// we expect the next two characters to be 0xFF
		if singleCharBuffer[0] == '\n' {
			buffer := [2]byte{0xFF, 0xFF} // explicitly initialize with non zero value
			bufferSlice := buffer[:]

			// seemingly every line ends with two \x00\x00
			n, err = c.telnetConn.Read(bufferSlice)
			if err != nil {
				return "", err
			}
			if n != 2 || !bytes.Equal(bufferSlice, []byte{0x00, 0x00}) {
				return "", errors.New("failed to read \\x00\\x00")
			}

			// successfully got the two 0x00,
			// no need to append newline characters here
			break
		}
		// n == 1 && buffer[0] != '\n'
		_ = lineBuffer.WriteByte(singleCharBuffer[0])
	}

	return lineBuffer.String(), nil
}

// WriteLine writes a line to the external console and forces its execution by appending a \n
func (c *Conn) WriteLine(line string) (err error) {
	return c.guard(func() error {
		return c.unguardedWriteLine(line)
	})
}

// WriteLine writes a line to the external console and forces its execution by appending a \n
func (c *Conn) unguardedWriteLine(line string) error {
	if c.telnetConn == nil {
		return errors.New("telnet connection is nil")
	}

	stream := []byte(line + "\n")

	for len(stream) > 0 {
		n, err := c.telnetConn.Write(stream)
		if err != nil {
			return err
		}
		stream = stream[n:]
	}
	return nil
}

func (c *Conn) retry(f func() (bool, error)) error {
	t, drained := newTimer(0)
	defer closeTimer(t, &drained)
	i := 0
	var wait time.Duration
	for {
		// retry
		select {
		case <-t.C:
			drained = true
		case <-c.ctx.Done():
			return c.ctx.Err()
		}

		retry, err := f()
		if err == nil {
			return nil
		}
		if !retry {
			return err
		}

		i++
		wait = c.backoff(i)
		resetTimer(t, wait, &drained)
	}
}

func (c *Conn) connect() error {
	// reconnect tcp connection
	telnetConn, err := telnet.DialTo(c.address)
	if err != nil {
		return err
	}

	// update internal state
	c.telnetConn = telnetConn
	return nil
}

func (c *Conn) reconnect() error {

	if c.telnetConn != nil {
		_ = c.logout()
		_ = c.telnetConn.Close()
	}

	// keep track of the last error that was returned
	return c.retry(func() (retry bool, err error) {
		err = c.connect()
		if err != nil {
			return true, err
		}
		defer func() {
			if err != nil {
				c.telnetConn.Close()
				c.telnetConn = nil
			}
		}()

		err = c.authenticate()
		if err == nil {
			return false, nil
		}

		if errors.Is(err, ErrAuthenticationFailed) {
			return false, err
		}

		return true, err
	})

}

// authenticate in the external console
func (c *Conn) authenticate() (err error) {
	password := c.password

	line, err := c.unguardedReadLine()
	if err != nil {
		// forward network error
		return err
	}

	if line != PasswordLine {
		return fmt.Errorf("%w: could not find password request line: %s", ErrAuthenticationFailed, line)
	}

	err = c.unguardedWriteLine(password)
	if err != nil {
		// forward network error
		return err
	}

	line, err = c.unguardedReadLine()
	if err != nil {
		// forward network error
		return err
	}

	if line != AuthenticationSuccessLine {
		return fmt.Errorf("%w: %s", ErrAuthenticationFailed, line)
	}

	for _, cmd := range c.authCommandList {
		err = c.unguardedWriteLine(cmd)
		if err != nil {
			return err
		}
	}
	return nil
}
