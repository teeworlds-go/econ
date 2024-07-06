# econ

[![Go Reference](https://pkg.go.dev/badge/github.com/teeworlds-go/econ.svg)](https://pkg.go.dev/github.com/teeworlds-go/econ) [![Go Report Card](https://goreportcard.com/badge/github.com/teeworlds-go/econ)](https://goreportcard.com/report/github.com/teeworlds-go/econ)

econ is a library that can be used to connect to Teeworlds external consoles in order to introduce some kind of automation based on the server's output.

## Installation

```bash
// the latest tagged release
go get github.com/teeworlds-go/econ@latest

// bleeding edge version
go get github.com/teeworlds-go/econ@master
```

## Example usage

This example shows a somewhat minimal use case where a joining user's id is extracted from the server's output and a message is sent to the server that contains the id.

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"

	"github.com/teeworlds-go/econ"
)

func main() {
	log.Println("starting application...")
	var (
		address     = "localhost:8403"
		password    = "12345"
		ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		wg          sync.WaitGroup
	)
	defer func() {
		// cancel context first, then wait for all goroutines to finish
		cancel()
		log.Println("waiting for all goroutines to finish")
		wg.Wait()
		log.Println("all goroutines finished")
	}()

	log.Printf("connecting to %s", address)
	conn, err := econ.DialTo(address, password, econ.WithContext(ctx))
	if err != nil {
		panic(err)
	}
	// safeguard in order to always close the connection
	defer func() {
		log.Printf("closing application...")
		// close connection first
		log.Println("closing connection...")
		_ = conn.Close()
		log.Println("connection closed")
	}()

	var (
		lineChan    = make(chan string)
		commandChan = make(chan string)
	)

	wg.Add(2) // 2 goroutines are started
	go asyncReadLine(ctx, &wg, conn, lineChan)
	go asyncWriteLine(ctx, &wg, conn, commandChan)

	log.Println("started application")
	for {
		line, ok := tryRead(ctx, lineChan)
		if !ok {
			break
		}

		// do stuff
		id, ok := playerID(line)
		if !ok {
			continue
		}

		command := fmt.Sprintf("say player with id %s joined", id)

		ok = tryWrite(ctx, commandChan, command)
		if !ok {
			break
		}
	}
}

var (
	// 0: full 1: ID 2: IPv4
	ddnetJoinRegex = regexp.MustCompile(`(?i)player has entered the game\. ClientID=([\d]+) addr=[^\d]{0,2}([\d]{1,3}\.[\d]{1,3}\.[\d]{1,3}\.[\d]{1,3})[^\d]{0,2}`)

	// 0: full 1: ID 2: IPv4 3: port 4: version 5: name 6: clan 7: country
	zCatchJoinRegex = regexp.MustCompile(`(?i)id=([\d]+) addr=([a-fA-F0-9\.\:\[\]]+):([\d]+) version=(\d+) name='(.{0,20})' clan='(.{0,16})' country=([-\d]+)$`)

	// 0: full 1: ID 2: IPv4
	vanillaJoinRegex = regexp.MustCompile(`(?i)player is ready\. ClientID=([\d]+) addr=[^\d]{0,2}([\d]{1,3}\.[\d]{1,3}\.[\d]{1,3}\.[\d]{1,3})[^\d]{0,2}`)

	joinRegexList = []*regexp.Regexp{
		ddnetJoinRegex,
		zCatchJoinRegex,
		vanillaJoinRegex,
	}
)

func playerID(line string) (id string, ok bool) {
	var matches []string
	for _, regex := range joinRegexList {
		if matches = regex.FindStringSubmatch(line); len(matches) > 0 {
			id = matches[1]
			return id, true
		}
	}
	return "", false
}

func tryRead(ctx context.Context, lineChan <-chan string) (line string, ok bool) {
	select {
	case <-ctx.Done():
		return "", false
	case line, ok = <-lineChan:
		return line, ok
	}
}

func tryWrite(ctx context.Context, commandChan chan<- string, command string) (ok bool) {
	select {
	case <-ctx.Done():
		return false
	case commandChan <- command:
		return true
	}
}

func asyncReadLine(ctx context.Context, wg *sync.WaitGroup, conn *econ.Conn, lineChan chan<- string) {
	defer func() {
		close(lineChan)
		wg.Done()
		log.Println("line reader closed")
	}()

	var (
		line string
		err  error
	)
	for {
		select {
		case <-ctx.Done():
			log.Printf("closing line reader: %v", ctx.Err())
			return
		default:
			line, err = conn.ReadLine()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					log.Printf("closing line reader: %v", err)
					return
				}
				log.Printf("failed to read line: %v", err)
			}
			lineChan <- line
		}
	}
}

func asyncWriteLine(ctx context.Context, wg *sync.WaitGroup, conn *econ.Conn, commandChan <-chan string) {
	defer func() {
		wg.Done()
		log.Println("command writer closed")
	}()

	var err error
	for {
		select {
		case <-ctx.Done():
			log.Printf("closing command writer: %v", ctx.Err())
			return
		case command, ok := <-commandChan:
			if !ok {
				log.Println("command channel closed")
				return
			}
			err = conn.WriteLine(command)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					log.Printf("closing command writer: %v", ctx.Err())
					return
				}
				log.Printf("failed to write line: %v", err)
			}
		}
	}
}
```
