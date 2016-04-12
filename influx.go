package main

import (
	"fmt"
	"log"
	"time"

	client "github.com/influxdata/influxdb/client/v2"
)

type Sender func(string, map[string]string, map[string]interface{}, time.Time) error

// InfluxConfig defines connection requirements
type InfluxConfig struct {
	Host, Username, Password string
	Port                     int
	Batch                    client.BatchPointsConfig
}

func (cfg *InfluxConfig) Connect() (client.Client, error) {
	url := fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port)

	conf := client.HTTPConfig{
		Addr:     url,
		Username: cfg.Username,
		Password: cfg.Password,
	}

	conn, err := client.NewHTTPClient(conf)
	if err != nil {
		return conn, err
	}

	_, _, err = conn.Ping(time.Second * 10)
	return conn, err
}

func (cfg *InfluxConfig) NewSender(batchSize, queueSize, period int) (Sender, error) {
	if debug {
		log.Println("Connecting to:", cfg.Host)
	}
	pts := make(chan *client.Point, queueSize)
	conn, err := cfg.Connect()
	if err != nil {
		return nil, fmt.Errorf("failed connecting to: %s", cfg.Host)
	}
	if debug {
		log.Println("Connected to:", cfg.Host)
	}

	bp, err := client.NewBatchPoints(cfg.Batch)
	if err != nil {
		return nil, err
	}

	go func() {
		delay := time.Duration(period) * time.Second
		tick := time.Tick(delay)
		count := 0
		for {
			select {
			case <-tick:
				log.Println("influxdb tick:", err)
				if len(bp.Points()) == 0 {
					continue
				}
			case p := <-pts:
				bp.AddPoint(p)
				count++
				if count < batchSize {
					continue
				}
			}
			for {
				if err := conn.Write(bp); err != nil {
					log.Println("influxdb write error:", err)
					time.Sleep(30 * time.Second)
					continue
				}
				bp, err = client.NewBatchPoints(cfg.Batch)
				if err != nil {
					log.Println("influxdb batchpoints error:", err)
				}
				count = 0
				break
			}
		}
	}()

	//return func(pt *client.Point) error {
	return func(key string, tags map[string]string, val map[string]interface{}, ts time.Time) error {
		pt, err := client.NewPoint(key, tags, val, ts)
		if err != nil {
			return err
		}
		pts <- pt
		return nil
	}, nil
}
