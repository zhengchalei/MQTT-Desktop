package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type mqttSession struct {
	mu      sync.Mutex
	closed  bool
	p       profile
	legacy  mqtt.Client
	modern  *autopaho.ConnectionManager
	cancel  context.CancelFunc
	state   func(connectionState)
	warning func(error)
	receive func(message)
}

func tlsConfig(p profile) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if p.CA != "" {
		data, err := os.ReadFile(p.CA)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("CA 文件不包含有效 PEM 证书")
		}
		cfg.RootCAs = roots
	}
	if p.Cert != "" {
		cert, err := tls.LoadX509KeyPair(p.Cert, p.Key)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}
func (s *mqttSession) subscriptions() []subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]subscription(nil), s.p.Subscriptions...)
}
func (s *mqttSession) received(topic string, payload []byte, qos byte, retain bool) {
	m := message{Connection: s.p.ID, At: time.Now(), Direction: "接收", Topic: topic, QoS: qos, Retain: retain, OriginalBytes: len(payload)}
	if len(payload) > maxPayloadBytes {
		payload = payload[:maxPayloadBytes]
		m.Truncated = true
	}
	m.Payload = append([]byte(nil), payload...)
	s.receive(m)
}
func (s *mqttSession) connect() error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return context.Canceled
	}
	if err := s.p.validate(); err != nil {
		return err
	}
	cfg, err := tlsConfig(s.p)
	if err != nil {
		return err
	}
	timeout := time.Duration(s.p.Timeout) * time.Second
	if s.p.Version == "3.1.1" {
		u, _ := url.Parse(s.p.URL)
		if u.Scheme == "mqtts" {
			u.Scheme = "ssl"
		}
		opts := mqtt.NewClientOptions().AddBroker(u.String()).SetClientID(s.p.ClientID).SetUsername(s.p.Username).SetPassword(s.p.Password).SetTLSConfig(cfg).SetCleanSession(s.p.Clean).SetKeepAlive(time.Duration(s.p.KeepAlive) * time.Second).SetConnectTimeout(timeout).SetWriteTimeout(timeout).SetAutoReconnect(s.p.Reconnect).SetConnectRetry(false).SetMaxReconnectInterval(10 * time.Second)
		opts.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) { s.received(m.Topic(), m.Payload(), m.Qos(), m.Retained()) })
		opts.SetOnConnectHandler(func(c mqtt.Client) {
			s.state(connectionState{Phase: phaseConnected})
			go func() {
				for _, sub := range s.subscriptions() {
					t := c.Subscribe(sub.Topic, sub.QoS, nil)
					if !t.WaitTimeout(timeout) {
						s.warn(fmt.Errorf("订阅超时"))
					} else if t.Error() != nil {
						s.warn(fmt.Errorf("订阅失败：%w", t.Error()))
					}
				}
			}()
		})
		opts.SetConnectionLostHandler(func(c mqtt.Client, e error) {
			if c.IsConnectionOpen() {
				return
			}
			if s.p.Reconnect {
				s.state(connectionState{Phase: phaseReconnecting, Detail: e.Error()})
			} else {
				s.state(connectionState{Phase: phaseDisconnected, Detail: e.Error()})
			}
		})
		if s.p.WillTopic != "" {
			opts.SetWill(s.p.WillTopic, s.p.WillPayload, s.p.WillQoS, s.p.WillRetain)
		}
		c := mqtt.NewClient(opts)
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return context.Canceled
		}
		s.legacy = c
		s.mu.Unlock()
		t := c.Connect()
		if !t.WaitTimeout(timeout + time.Second) {
			c.Disconnect(0)
			return fmt.Errorf("连接超时")
		}
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			c.Disconnect(0)
			return context.Canceled
		}
		return t.Error()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return context.Canceled
	}
	s.cancel = cancel
	s.mu.Unlock()
	u, _ := url.Parse(s.p.URL)
	if u.Scheme == "mqtts" {
		u.Scheme = "tls"
	}
	conf := autopaho.ClientConfig{ServerUrls: []*url.URL{u}, TlsCfg: cfg, KeepAlive: uint16(s.p.KeepAlive), CleanStartOnInitialConnection: s.p.Clean, SessionExpiryInterval: uint32(s.p.SessionExpiry), ConnectTimeout: timeout, ConnectUsername: s.p.Username, ConnectPassword: []byte(s.p.Password), ReconnectBackoff: func(n int) time.Duration { return time.Duration(min(n, 10)) * time.Second }}
	conf.ClientConfig = paho.ClientConfig{ClientID: s.p.ClientID, OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(r paho.PublishReceived) (bool, error) {
		s.received(r.Packet.Topic, r.Packet.Payload, r.Packet.QoS, r.Packet.Retain)
		return true, nil
	}}}
	firstResult := make(chan error, 1)
	var established atomic.Bool
	disconnected := func(err error) {
		if !established.Load() {
			return
		}
		phase := phaseDisconnected
		if s.p.Reconnect {
			phase = phaseReconnecting
		}
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		s.state(connectionState{Phase: phase, Detail: detail})
	}
	conf.OnClientError = func(err error) { disconnected(err) }
	conf.OnServerDisconnect = func(d *paho.Disconnect) {
		disconnected(fmt.Errorf("Broker 断开连接，原因码 0x%02x", d.ReasonCode))
	}
	// Advertise a bounded packet size; oversized payloads are truncated in the local viewer too.
	conf.ConnectPacketBuilder = func(c *paho.Connect, _ *url.URL) (*paho.Connect, error) {
		limit := uint32(2 << 20)
		if c.Properties == nil {
			c.Properties = &paho.ConnectProperties{}
		}
		c.Properties.MaximumPacketSize = &limit
		return c, nil
	}
	if s.p.WillTopic != "" {
		conf.WillMessage = &paho.WillMessage{Topic: s.p.WillTopic, Payload: []byte(s.p.WillPayload), QoS: s.p.WillQoS, Retain: s.p.WillRetain}
	}
	conf.OnConnectionUp = func(cm *autopaho.ConnectionManager, _ *paho.Connack) {
		established.Store(true)
		select {
		case firstResult <- nil:
		default:
		}
		s.state(connectionState{Phase: phaseConnected})
		go func() {
			for _, sub := range s.subscriptions() {
				if e := subscribe5(cm, sub, timeout); e != nil {
					s.warn(fmt.Errorf("订阅失败：%w", e))
				}
			}
		}()
	}
	conf.OnConnectError = func(e error) {
		if !established.Load() {
			// Autopaho retries first connections by default. Surface the first failure and stop those retries.
			s.state(connectionState{Phase: phaseFailed, Detail: e.Error()})
			select {
			case firstResult <- e:
			default:
			}
			cancel()
		} else {
			s.state(connectionState{Phase: phaseReconnecting, Detail: e.Error()})
		}
	}
	conf.OnConnectionDown = func() bool {
		if s.p.Reconnect {
			s.state(connectionState{Phase: phaseReconnecting})
		} else {
			s.state(connectionState{Phase: phaseDisconnected})
		}
		return s.p.Reconnect
	}
	cm, err := autopaho.NewConnection(ctx, conf)
	if err != nil {
		cancel()
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return context.Canceled
	}
	s.modern = cm
	s.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err = <-firstResult:
		if err != nil {
			cancel()
		}
		return err
	case <-ctx.Done():
		select {
		case err = <-firstResult:
			return err
		default:
			return ctx.Err()
		}
	case <-timer.C:
		cancel()
		return fmt.Errorf("连接超时")
	}
}

func (s *mqttSession) warn(err error) {
	if s.warning != nil {
		s.warning(err)
	}
}
func subscribe5(cm *autopaho.ConnectionManager, sub subscription, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ack, err := cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: sub.Topic, QoS: sub.QoS}}})
	if err != nil {
		return err
	}
	for _, code := range ack.Reasons {
		if code >= 128 {
			return fmt.Errorf("SUBACK 原因码 0x%02x", code)
		}
	}
	return nil
}
func (s *mqttSession) subscribe(sub subscription, remove bool) error {
	s.mu.Lock()
	c, cm := s.legacy, s.modern
	s.mu.Unlock()
	timeout := time.Duration(s.p.Timeout) * time.Second
	if c != nil {
		var t mqtt.Token
		if remove {
			t = c.Unsubscribe(sub.Topic)
		} else {
			t = c.Subscribe(sub.Topic, sub.QoS, nil)
		}
		if !t.WaitTimeout(timeout) {
			return fmt.Errorf("订阅操作超时")
		}
		if t.Error() != nil {
			return t.Error()
		}
		if ack, ok := t.(*mqtt.SubscribeToken); ok {
			for _, code := range ack.Result() {
				if code == 128 {
					return fmt.Errorf("Broker 拒绝了订阅")
				}
			}
		}
	} else if cm != nil {
		if remove {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			ack, err := cm.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{sub.Topic}})
			if err != nil {
				return err
			}
			for _, code := range ack.Reasons {
				if code >= 128 {
					return fmt.Errorf("UNSUBACK 原因码 0x%02x", code)
				}
			}
		} else if err := subscribe5(cm, sub, timeout); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("连接尚未就绪")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.p.Subscriptions[:0]
	for _, v := range s.p.Subscriptions {
		if v.Topic != sub.Topic {
			out = append(out, v)
		}
	}
	if !remove {
		out = append(out, sub)
	}
	s.p.Subscriptions = out
	return nil
}
func (s *mqttSession) publish(topic string, payload []byte, qos byte, retain bool) error {
	if err := validTopic(topic, false); err != nil {
		return err
	}
	s.mu.Lock()
	c, cm := s.legacy, s.modern
	s.mu.Unlock()
	timeout := time.Duration(s.p.Timeout) * time.Second
	if c != nil {
		t := c.Publish(topic, qos, retain, payload)
		if !t.WaitTimeout(timeout) {
			return fmt.Errorf("发布超时")
		}
		return t.Error()
	}
	if cm == nil {
		return fmt.Errorf("连接尚未就绪")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ack, err := cm.Publish(ctx, &paho.Publish{Topic: topic, Payload: payload, QoS: qos, Retain: retain})
	if err == nil && ack != nil && ack.ReasonCode >= 128 {
		return fmt.Errorf("发布原因码 0x%02x", ack.ReasonCode)
	}
	return err
}
func (s *mqttSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	c, cm, cancel := s.legacy, s.modern, s.cancel
	s.mu.Unlock()
	if c != nil {
		c.Disconnect(100)
	}
	if cm != nil {
		ctx, done := context.WithTimeout(context.Background(), time.Second)
		_ = cm.Disconnect(ctx)
		done()
	}
	if cancel != nil {
		cancel()
	}
}
