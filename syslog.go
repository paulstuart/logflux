package main

import (
	"bufio"
	"fmt"
	"time"

	syslog "gopkg.in/mcuadros/go-syslog.v2"
)

type Filter struct {
	Facility, Severity int
	Hostname           string
}

type SLOG struct {
	Facility, Severity int
	Hostname, Content  string
	TS                 time.Time
}

type sChan chan SLOG

func (s sChan) Read(p []byte) (n int, err error) {
	const layout = "Jan 2 15:04:05"
	select {
	case slog := <-s:
		msg := fmt.Sprintf("%s %s %s\n", slog.TS.Format(layout), slog.Hostname, slog.Content)
		n = copy(p, msg)
		fmt.Printf("READ (%d/%d): %s\n", len(p), n, string(p))
		return
	}
}

const (
	maxSLOGs = 65535
)

var (
	facilities []Filter
)

// sysTime parses syslog time which is missing the year
func sysTime(s string) (time.Time, error) {
	const l = "Jan 1 15:04:05 2006"
	return time.Parse(l, fmt.Sprintf("%s %d", s, time.Now().Year))
}

// Listen starts a syslog server and sends logs to the returned bufio.Reader
func Listen(ip string, tcp, udp int) (*bufio.Reader, error) {
	channel := make(syslog.LogPartsChannel)
	slogs := make(sChan, maxSLOGs)

	go func() {
		for parts := range channel {
			slog := SLOG{
				parts["facility"].(int),
				parts["severity"].(int),
				parts["hostname"].(string),
				parts["content"].(string),
				parts["timestamp"].(time.Time),
			}
			if len(facilities) == 0 {
				slogs <- slog
				continue
			}
			for _, f := range facilities {
				if f.Facility != slog.Facility {
					continue
				}
				if f.Severity > 0 && slog.Severity > f.Severity {
					continue
				}
				if debug {
					fmt.Println("SLOG:", slog)
				}
				slogs <- slog
				break
			}
		}

	}()
	server := syslog.NewServer()
	//server.SetFormat(syslog.RFC5424)
	server.SetFormat(syslog.RFC3164) // MacOS
	server.SetHandler(syslog.NewChannelHandler(channel))
	if udp > 0 {
		if err := server.ListenUDP(fmt.Sprintf("%s:%d", ip, udp)); err != nil {
			return nil, err
		}
	}
	if tcp > 0 {
		if err := server.ListenTCP(fmt.Sprintf("%s:%d", ip, tcp)); err != nil {
			return nil, err
		}
	}

	server.Boot()

	return bufio.NewReader(slogs), nil
}
