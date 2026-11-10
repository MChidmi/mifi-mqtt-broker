// Minimal MQTT broker for the modem's LAN (mochi-mqtt). Static ARM binary for Android.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

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
	// Allow all connections — this broker is only reachable on the local LAN.
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		log.Fatal(err)
	}

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
	log.Printf("mqtt broker listening tcp=%s ws=%s", tcpAddr, wsAddr)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = server.Close()
}
