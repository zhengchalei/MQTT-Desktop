package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	broker "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

func testCertificate(t *testing.T) (tls.Certificate, string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "key.pem")
	if err = os.WriteFile(certPath, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	return cert, certPath, keyPath
}
func startBroker(t *testing.T, scheme string, mutual bool) (string, string, string) {
	address, ca, key, _ := startBrokerInstance(t, scheme, mutual)
	return address, ca, key
}
func startBrokerInstance(t *testing.T, scheme string, mutual bool) (string, string, string, *broker.Server) {
	t.Helper()
	srv := broker.New(&broker.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := srv.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	var cfg *tls.Config
	var ca, key string
	if scheme == "mqtts" || scheme == "wss" {
		var cert tls.Certificate
		cert, ca, key = testCertificate(t)
		cfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		if mutual {
			data, _ := os.ReadFile(ca)
			cfg.ClientCAs = x509.NewCertPool()
			cfg.ClientCAs.AppendCertsFromPEM(data)
			cfg.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}
	addr := "127.0.0.1:0"
	if scheme == "ws" || scheme == "wss" {
		reserve, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		addr = reserve.Addr().String()
		reserve.Close()
		if err = srv.AddListener(listeners.NewWebsocket(listeners.Config{ID: "ws", Address: addr, TLSConfig: cfg})); err != nil {
			t.Fatal(err)
		}
	} else {
		listener := listeners.NewTCP(listeners.Config{ID: "tcp", Address: addr, TLSConfig: cfg})
		if err := srv.AddListener(listener); err != nil {
			t.Fatal(err)
		}
		addr = listener.Address()
	}
	if err := srv.Serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	path := ""
	if scheme == "ws" || scheme == "wss" {
		path = "/mqtt"
	}
	return scheme + "://" + addr + path, ca, key, srv
}
func TestMQTTRoundTrip(t *testing.T) {
	for _, version := range []string{"3.1.1", "5.0"} {
		for _, scheme := range []string{"mqtt", "mqtts", "ws", "wss"} {
			t.Run(version+"/"+scheme, func(t *testing.T) {
				address, ca, _ := startBroker(t, scheme, false)
				p := newProfile()
				p.URL = address
				p.Version = version
				p.CA = ca
				p.Timeout = 3
				p.Reconnect = false
				received := make(chan message, 20)
				s := &mqttSession{p: p, state: func(connectionState) {}, receive: func(m message) { received <- m }}
				defer s.close()
				if err := s.connect(); err != nil {
					t.Fatal(err)
				}
				sub := subscription{Topic: "test/+/data", QoS: 2}
				if err := s.subscribe(sub, false); err != nil {
					t.Fatal(err)
				}
				for qos := byte(0); qos < 3; qos++ {
					payload := []byte{0, 1, 2, 255, qos}
					if err := s.publish("test/device/data", payload, qos, false); err != nil {
						t.Fatal(err)
					}
					select {
					case m := <-received:
						if !bytes.Equal(m.Payload, payload) || m.QoS != qos || m.Topic != "test/device/data" {
							t.Fatalf("unexpected message: %+v", m)
						}
					case <-time.After(3 * time.Second):
						t.Fatal("no message")
					}
				}
				if err := s.subscribe(sub, true); err != nil {
					t.Fatal(err)
				}
				if err := s.publish("test/device/data", []byte("after unsubscribe"), 1, false); err != nil {
					t.Fatal(err)
				}
				select {
				case <-received:
					t.Fatal("received after unsubscribe")
				case <-time.After(80 * time.Millisecond):
				}
			})
		}
	}
}
func TestMutualTLS(t *testing.T) {
	for _, version := range []string{"3.1.1", "5.0"} {
		t.Run(version, func(t *testing.T) {
			address, cert, key := startBroker(t, "mqtts", true)
			p := newProfile()
			p.URL = address
			p.Version = version
			p.CA = cert
			p.Cert = cert
			p.Key = key
			p.Reconnect = false
			s := &mqttSession{p: p, state: func(connectionState) {}, receive: func(message) {}}
			defer s.close()
			if err := s.connect(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestClosedSessionDoesNotConnect(t *testing.T) {
	s := &mqttSession{p: newProfile(), state: func(connectionState) {}, receive: func(message) {}}
	s.close()
	if err := s.connect(); err == nil {
		t.Fatal("closed session connected")
	}
}

func TestRetainedMessage(t *testing.T) {
	for _, version := range []string{"3.1.1", "5.0"} {
		t.Run(version, func(t *testing.T) {
			address, _, _ := startBroker(t, "mqtt", false)
			p := newProfile()
			p.URL = address
			p.Version = version
			p.Reconnect = false
			received := make(chan message, 4)
			s := &mqttSession{p: p, state: func(connectionState) {}, receive: func(m message) { received <- m }}
			defer s.close()
			if err := s.connect(); err != nil {
				t.Fatal(err)
			}
			if err := s.publish("retain/state", []byte("online"), 1, true); err != nil {
				t.Fatal(err)
			}
			if err := s.subscribe(subscription{Topic: "retain/state", QoS: 1}, false); err != nil {
				t.Fatal(err)
			}
			select {
			case m := <-received:
				if !m.Retain || string(m.Payload) != "online" {
					t.Fatal("retained state missing")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("retained message not received")
			}
		})
	}
}

func TestMQTTMessageVolume(t *testing.T) {
	address, _, _ := startBroker(t, "mqtt", false)
	p := newProfile()
	p.URL = address
	p.Version = "5.0"
	p.Reconnect = false
	received := make(chan message, 6000)
	s := &mqttSession{p: p, state: func(connectionState) {}, receive: func(m message) { received <- m }}
	defer s.close()
	if err := s.connect(); err != nil {
		t.Fatal(err)
	}
	if err := s.subscribe(subscription{Topic: "volume/#", QoS: 1}, false); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	payload := bytes.Repeat([]byte("x"), 2048)
	for i := 0; i < 5000; i++ {
		if err := s.publish("volume/device", payload, 1, false); err != nil {
			t.Fatal(err)
		}
	}
	buffer := &messageBuffer{}
	for i := 0; i < 5000; i++ {
		select {
		case m := <-received:
			buffer.add(m)
		case <-time.After(3 * time.Second):
			t.Fatal("missing message")
		}
	}
	if len(buffer.Items) != 2000 || buffer.Evicted != 3000 || buffer.Bytes > maxCacheBytes {
		t.Fatal("cache did not remain bounded")
	}
	t.Logf("5000 MQTT messages, 2048-byte payload: %s; cache=%d messages, %d bytes", time.Since(start), len(buffer.Items), buffer.Bytes)
}
