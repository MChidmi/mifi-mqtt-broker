// Minimal MQTT broker for the modem's LAN (mochi-mqtt), with an activity LED.
// Static ARM binary for Android.
//
// The router's charge LED is two colours — bat1 = red, batl2 = green.
//   * It flashes RED whenever a message passes through the broker in either
//     direction (a client publishes in, or the broker delivers out).
//   * Between flashes it's GREEN while >=1 client is connected, otherwise off.
//   * Publishing to topic "led" overrides the base colour: green|red|off|auto.
//   * A web toggle writes LED_CONF ("ENABLED=0/1"); when disabled the broker
//     stops touching the LEDs and leaves them to the firmware's battery driver.
// A fast ticker re-asserts the LED so the firmware can't steal it.
package main

import (
	"bytes"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

var (
	ledRed   = envOr("LED_RED", "/sys/class/leds/bat1/brightness")
	ledGreen = envOr("LED_GREEN", "/sys/class/leds/batl2/brightness")
	ledTopic = envOr("LED_TOPIC", "led")
	ledConf  = envOr("LED_CONF", "/data/local/tmp/webui/led.conf")
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func wled(path, v string) { _ = os.WriteFile(path, []byte(v), 0644) }

func applyColor(c string) {
	switch c {
	case "red":
		wled(ledRed, "255")
		wled(ledGreen, "0")
	case "green":
		wled(ledRed, "0")
		wled(ledGreen, "255")
	default:
		wled(ledRed, "0")
		wled(ledGreen, "0")
	}
}

// notifyEnabled reads the web-settable flag. Missing file = enabled.
func notifyEnabled() bool {
	b, err := os.ReadFile(ledConf)
	if err != nil {
		return true
	}
	return !strings.Contains(string(b), "ENABLED=0")
}

var lastMsg atomic.Int64 // unix-nano of the most recent message, for the flash

func touch() { lastMsg.Store(time.Now().UnixNano()) }

type led struct {
	mu      sync.Mutex
	manual  bool
	color   string
	clients int
}

func (l *led) base() string {
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
func (l *led) set(c string) { l.mu.Lock(); l.manual = true; l.color = c; l.mu.Unlock() }
func (l *led) auto()        { l.mu.Lock(); l.manual = false; l.mu.Unlock() }
func (l *led) client(n int) {
	l.mu.Lock()
	l.clients += n
	if l.clients < 0 {
		l.clients = 0
	}
	l.mu.Unlock()
}

type ledHook struct {
	mqtt.HookBase
	l *led
}

func (h *ledHook) ID() string { return "led-activity" }

func (h *ledHook) Provides(b byte) bool {
	return bytes.Contains([]byte{
		mqtt.OnPublish, mqtt.OnPublished,
		mqtt.OnSessionEstablished, mqtt.OnDisconnect,
	}, []byte{b})
}

// incoming: a client published to the broker
func (h *ledHook) OnPublish(cl *mqtt.Client, pk packets.Packet) (packets.Packet, error) {
	touch()
	if pk.TopicName == ledTopic {
		switch strings.TrimSpace(strings.ToLower(string(pk.Payload))) {
		case "red", "green", "off":
			h.l.set(strings.ToLower(strings.TrimSpace(string(pk.Payload))))
		case "auto":
			h.l.auto()
		}
	}
	return pk, nil
}

// outgoing: the broker delivered a message to a subscriber
func (h *ledHook) OnPublished(cl *mqtt.Client, pk packets.Packet) { touch() }

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

	// Drive the LED: flash red on recent traffic, else the base colour.
	// Re-reads the enable flag so a web toggle takes effect live.
	go func() {
		t := time.NewTicker(60 * time.Millisecond)
		defer t.Stop()
		prevEnabled := true
		for range t.C {
			if !notifyEnabled() {
				if prevEnabled {
					applyColor("off") // release the LEDs back to the firmware
				}
				prevEnabled = false
				continue
			}
			prevEnabled = true
			if time.Since(time.Unix(0, lastMsg.Load())) < 130*time.Millisecond {
				applyColor("red")
			} else {
				applyColor(l.base())
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
	log.Printf("mqtt broker tcp=%s ws=%s  (LED: flash red on traffic; base green when connected; topic %q overrides)", tcpAddr, wsAddr, ledTopic)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	applyColor("off")
	_ = server.Close()
}
