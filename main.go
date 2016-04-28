package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"github.com/vjeantet/grok"
)

type Meta struct {
	Pattern, Key, Good, Tags string
	Valid                    map[string]string
}

// Translate controls how the map values are converted to a Point
type Translate struct {
	Pattern  string // Pattern to apply when parsing data
	Key      string // Lookup the value of Key, if found use it as point name, otherwise use Key itself
	TSLayout string // Timestamp Layout for parsing
	TSField  string // Field to be parsed for timestamp
	TFields  string // Space delimited list of tag fields to use
	VFields  string // Space delimited list of fiedlds to use for values
}

var (
	debug     bool
	listen    bool
	file      string
	directory = "patterns"
	tcp       = syslogPort
	udp       = syslogPort
	ip        = "0.0.0.0"
	filters   []Meta
	batchSize = 64
	queueSize = 8192
	period    = 60

	ErrNoMatch = fmt.Errorf("No match")
)

const (
	syslogPort = 514
)

func init() {
	flag.BoolVar(&debug, "d", debug, "debug")
	flag.BoolVar(&listen, "l", listen, "listen for syslog messages")
	flag.StringVar(&file, "f", file, "file to process")
	flag.StringVar(&ip, "i", ip, "ip to bind to")
	flag.IntVar(&tcp, "t", syslogPort, "tcp port")
	flag.IntVar(&udp, "u", syslogPort, "udp port")
	flag.StringVar(&directory, "p", directory, "Patterns dir")

	viper.AddConfigPath(".")
	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.SetDefault("Influxdb.Batch.RetentionPolicy", "default")
}

func unquoted(s string) string {
	if len(s) > 0 && s[0] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// pretty print for debugging
func pp(x interface{}) string {
	b, _ := json.MarshalIndent(x, " ", " ")
	return fmt.Sprintln(string(b))
}

// numerical returns the parsed data type in its native form
func numerical(s string) interface{} {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	if i, err := strconv.ParseInt(s, 0, 64); err == nil {
		return i
	}
	return s
}

// AddPoint will convert the values of m into an influxdb Point and queue it to send
func AddPoint(m, tags map[string]string, t Translate, send Sender) (err error) {
	found, ok := m[t.Key]
	if !ok {
		found = t.Key
	}
	for _, tag := range strings.Fields(t.TFields) {
		tags[tag] = m[tag]
	}
	f := make(map[string]interface{})
	for _, val := range strings.Fields(t.VFields) {
		f[val] = numerical(m[val])
	}

	var ts time.Time
	if len(t.TSLayout) > 0 && len(t.TSField) > 0 {
		if ts, err = time.Parse(t.TSLayout, m[t.TSField]); err != nil {
			return err
		}
	} else {
		ts = time.Now()
	}

	return send(found, tags, f, ts)
}

// refine will check to see if it is selected, then apply regex and capture
func refine(g *grok.Grok, data map[string]string, trans *Translate, valid map[string]string, ptrn, key, good, tags string) error {
	if valid != nil {
		for k, v := range valid {
			if data[k] != v {
				return ErrNoMatch
			}
		}
	}
	if len(ptrn) > 0 {
		matched, err := g.Parse(ptrn, data[key])
		if err != nil {
			return err
		}
		// TODO: this can overwrite existing values. Bug or Feature?
		for k, v := range matched {
			data[k] = unquoted(v)
		}
	}
	trans.VFields += " " + good
	trans.TFields += " " + tags
	return nil
}

// Parse will apply a filter and submit results to influxdb as a data point
func Parse(trans Translate, tags map[string]string, reader *bufio.Reader, s Sender) error {
	g, _ := grok.New()

	if len(directory) > 0 {
		if err := g.AddPatternsFromPath(directory); err != nil {
			return err
		}
	}

	process := func(m map[string]string) error {
		if debug {
			for k, v := range m {
				fmt.Println("Key:", k, "Value:", v)
			}
		}
		var err error
		t := trans
		for _, meta := range filters {
			if err = refine(g, m, &t, meta.Valid, meta.Pattern, meta.Key, meta.Good, meta.Tags); err == nil {
				break
			}
		}
		if err != nil {
			return err
		}

		return AddPoint(m, tags, t, s)
	}

	return g.ParseStream(reader, trans.Pattern, process)
}

func parseFile(file string, trans Translate, tags map[string]string, sender Sender) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	return Parse(trans, tags, reader, sender)
}

func main() {
	flag.Parse()

	if err := viper.ReadInConfig(); err != nil {
		panic(fmt.Errorf("Fatal error config file: %s \n", err))
	}
	mapping := Translate{
		viper.GetString("Pattern"),
		viper.GetString("Key"),
		viper.GetString("TSLayout"),
		viper.GetString("TSField"),
		viper.GetString("TFields"),
		viper.GetString("VFields"),
	}
	tags := viper.GetStringMapString("Tags")
	if err := viper.UnmarshalKey("Filters", &filters); err != nil {
		panic(err)
	}
	var influx InfluxConfig
	if err := viper.UnmarshalKey("Influxdb", &influx); err != nil {
		fmt.Printf("influx config error:", err)
	}

	sender, err := influx.NewSender(batchSize, queueSize, period)
	if err != nil {
		panic(err)
	}

	if listen {
		reader, err := Listen(ip, tcp, udp)
		if err != nil {
			panic(err)
		}
		for err == nil {
			err = Parse(mapping, tags, reader, sender)
		}
		if err != nil {
			panic(err)
		}
		return
	}
	if len(file) > 0 {
		if err := parseFile(file, mapping, tags, sender); err != nil {
			panic(err)
		}
	}
}
