// Minimal MQTT broker for the modem's LAN (mochi-mqtt), with an MQTT-driven
// notification LED. Static ARM binary for Android.
//
// LED: this router's charge LED is two colours — bat1 = red, batl2 = green.
// Idle:   green when >=1 client is connected, off when none.
// Notify: publish to topic "led" (LED_TOPIC) with one of:
//           green | red | off | auto | blink | blink:green | ok | alert
// A ticker re-asserts the LED so the firmware's battery driver can't steal it.
package main

import (
	"bytes"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

var (
	ledRed   = envOr("LED_RED", "/sys/class/leds/bat1/brightness")   // red channel
	ledGreen = envOr("LED_GREEN", "/sys/class/leds/batl2/brightness") // green channel
	ledTopic = envOr("LED_TOPIC", "led")
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func wled(path, v string) { _ = os.WriteFile(path, []byte(v), 0644) }

// applyColor never lights both channels at once (no yellow).
func applyColor(c string) {
	switch c {
	case "red":
		wled(ledRed, "255")
		wled(ledGreen, "0")
	case "green":
		wled(ledRed, "0")
		wled(ledGreen, "255")
	default: // off
		wled(ledRed, "0")
		wled(ledGreen, "0")
	}
}

type led struct {
	mu       sync.Mutex
	manual   bool
	color    string
	clients  int
	blinking bool
}

func (l *led) want() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.manual {
		return l.color
	}
	if l.clients > 0 {
		return "green"
	}
	return "off"
}

func (l *led) set(c string) { l.mu.Lock(); l.manual = true; l.color = c; l.mu.Unlock(); applyColor(c) }
func (l *led) auto()        { l.mu.Lock(); l.manual = false; l.mu.Unlock(); applyColor(l.want()) }

func (l *led) client(n int) {
	l.mu.Lock()
	l.clients += n
	if l.clients < 0 {
		l.clients = 0
	}
	l.mu.Unlock()
	applyColor(l.want())
}

func (l *led) blink(c string) {
	l.mu.Lock()
	if l.blinking {
		l.mu.Unlock()
		return
	}
	l.blinking = true
	l.mu.Unlock()
	go func() {
		for i := 0; i < 6; i++ {
			applyColor(c)
			time.Sleep(160 * time.Millisecond)
			applyColor("off")
			time.Sleep(160 * time.Millisecond)
		}
		l.mu.Lock()
		l.blinking = false
		l.mu.Unlock()
		applyColor(l.want())
	}()
}

type ledHook struct {
	mqtt.HookBase
	l *led
}

func (h *ledHook) ID() string { return "led-notify" }

func (h *ledHook) Provides(b byte) bool {
	return bytes.Contains([]byte{mqtt.OnPublish, mqtt.OnSessionEstablished, mqtt.OnDisconnect}, []byte{b})
}

func (h *ledHook) OnPublish(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error) {
	if pk.TopicName == ledTopic {
		switch strings.TrimSpace(strings.ToLower(string(pk.Payload))) {
		case "red", "green", "off":
			h.l.set(strings.ToLower(string(pk.Payload)))
		case "auto":
			h.l.auto()
		case "blink", "blink:red", "alert":
			h.l.blink("red")
		case "blink:green", "ok":
			h.l.blink("green")
		}
	}
	return pk, nil
}

func (h *ledHook) OnSessionEstablished(cl *mqtt.Client, pk packets.Packet) { h.l.client(1) }
func (h *ledHook) OnDisconnect(cl *mqtt.Client, err error, expire bool)    { h.l.client(-1) }

func main() {
	tcpAddr := ":1883"
	wsAddr := ":9001"
	if len(os.Args) > 1 {
		tcpAddr = os.Args[1]
	}
	if len(os.Args) > 2 {
		wsAddr = os.Args[2]
	}

	server := mqtt.New(&mqtt.Options{})
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		log.Fatal(err)
	}

	l := &led{}
	if err := server.AddHook(&ledHook{l: l}, nil); err != nil {
		log.Fatal(err)
	}
	applyColor("off")

	// Re-assert the LED periodically so the firmware's charge-LED driver
	// doesn't overwrite our colour.
	go func() {
		t := time.NewTicker(1500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			l.mu.Lock()
			blinking := l.blinking
			l.mu.Unlock()
			if !blinking {
				applyColor(l.want())
			}
		}
	}()

	if err := server.AddListener(listeners.NewTCP(listeners.Config{ID: "tcp", Address: tcpAddr})); err != nil {
		log.Fatal(err)
	}
	if wsAddr != "-" {
		if err := server.AddListener(listeners.NewWebsocket(listeners.Config{ID: "ws", Address: wsAddr})); err != nil {
			log.Printf("websocket listener failed: %v", err)
		}
	}

	go func() {
		if err := server.Serve(); err != nil {
			log.Fatal(err)
		}
	}()
	log.Printf("mqtt broker tcp=%s ws=%s  (LED notify on topic %q: green|red|off|auto|blink|ok)", tcpAddr, wsAddr, ledTopic)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	applyColor("off")
	_ = server.Close()
}
