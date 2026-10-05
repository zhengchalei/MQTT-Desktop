package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const maxMessages = 2000
const maxCacheBytes = 16 << 20
const maxPayloadBytes = 1 << 20
const maxQueueBytes = 8 << 20

func messageBytes(m message) int { return len(m.Payload) + len(m.Topic) + 128 }

type connectionPhase string

const (
	phaseDisconnected connectionPhase = "未连接"
	phaseConnecting   connectionPhase = "连接中"
	phaseConnected    connectionPhase = "已连接"
	phaseReconnecting connectionPhase = "重连中"
	phaseFailed       connectionPhase = "连接失败"
)

type connectionState struct {
	Phase  connectionPhase
	Detail string
}

func (s connectionState) label() string {
	if s.Phase == "" {
		return string(phaseDisconnected)
	}
	return string(s.Phase)
}

type subscription struct {
	Topic, Alias, Color string
	QoS                 byte
}
type profile struct {
	ID, Name, URL, ClientID, Version, Username string
	Password                                   string `json:"-"`
	KeepAlive, Timeout, SessionExpiry          int
	Clean, Reconnect                           bool
	CA, Cert, Key                              string
	WillTopic, WillPayload                     string
	WillQoS                                    byte
	WillRetain, History                        bool
	Subscriptions                              []subscription
}

func newProfile() profile {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	return profile{ID: id, Name: "新连接", URL: "mqtt://127.0.0.1:1883", ClientID: "mygo-" + id, Version: "3.1.1", KeepAlive: 60, Timeout: 10, Clean: true, Reconnect: true}
}
func (p profile) validate() error {
	u, err := url.Parse(p.URL)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("请输入完整 Broker 地址，例如 mqtt://127.0.0.1:1883；认证请填写用户名和密码")
	}
	switch u.Scheme {
	case "mqtt", "mqtts", "ws", "wss":
	default:
		return fmt.Errorf("协议必须为 mqtt、mqtts、ws 或 wss")
	}
	if p.Name == "" || p.ClientID == "" {
		return fmt.Errorf("名称和 Client ID 不能为空")
	}
	if p.Version != "3.1.1" && p.Version != "5.0" {
		return fmt.Errorf("不支持的 MQTT 版本")
	}
	if p.KeepAlive < 1 || p.KeepAlive > 65535 || p.Timeout < 1 || p.Timeout > 120 || p.SessionExpiry < 0 {
		return fmt.Errorf("Keep Alive 为 1–65535 秒，超时为 1–120 秒，会话有效期不能为负")
	}
	if (p.Cert == "") != (p.Key == "") {
		return fmt.Errorf("客户端证书和私钥必须同时配置")
	}
	if p.WillTopic != "" {
		return validTopic(p.WillTopic, false)
	}
	return nil
}
func validTopic(topic string, filter bool) error {
	if topic == "" || len(topic) > 65535 || !utf8.ValidString(topic) || strings.ContainsRune(topic, 0) {
		return fmt.Errorf("主题为空或包含无效字符")
	}
	if !filter && strings.ContainsAny(topic, "+#") {
		return fmt.Errorf("发布主题不能包含通配符")
	}
	parts := strings.Split(topic, "/")
	for i, v := range parts {
		if strings.Contains(v, "#") && (v != "#" || i != len(parts)-1) || strings.Contains(v, "+") && v != "+" {
			return fmt.Errorf("+ 必须占据完整层级，# 必须位于最后一个层级")
		}
	}
	return nil
}
func topicMatches(filter, topic string) bool {
	if strings.HasPrefix(filter, "$share/") {
		parts := strings.SplitN(filter, "/", 3)
		if len(parts) != 3 {
			return false
		}
		filter = parts[2]
	}
	if strings.HasPrefix(topic, "$") && !strings.HasPrefix(filter, "$") {
		return false
	}
	f, t := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, v := range f {
		if v == "#" {
			return true
		}
		if i >= len(t) || v != "+" && v != t[i] {
			return false
		}
	}
	return len(f) == len(t)
}
func decodePayload(value, format string) ([]byte, error) {
	var b []byte
	var err error
	switch format {
	case "Hex":
		b, err = hex.DecodeString(strings.Join(strings.Fields(value), ""))
	case "Base64":
		b, err = base64.StdEncoding.DecodeString(value)
	case "JSON":
		if !json.Valid([]byte(value)) {
			return nil, fmt.Errorf("JSON 格式无效")
		}
		b = []byte(value)
	default:
		b = []byte(value)
	}
	if len(b) > maxPayloadBytes {
		return nil, fmt.Errorf("首版单条发布消息上限为 1 MiB")
	}
	return b, err
}
func displayPayload(b []byte, format string) string {
	switch format {
	case "Hex":
		return hex.EncodeToString(b)
	case "Base64":
		return base64.StdEncoding.EncodeToString(b)
	case "JSON":
		var out bytes.Buffer
		if json.Indent(&out, b, "", "  ") == nil {
			return out.String()
		}
	}
	return strings.ToValidUTF8(string(b), "�")
}

type message struct {
	ID               int64
	Connection       string
	At               time.Time
	Direction, Topic string
	QoS              byte
	Retain           bool
	Payload          []byte
	OriginalBytes    int
	Truncated        bool
}
type messageBuffer struct {
	Items          []message
	Bytes, Evicted int
	next           int64
	counts         map[string]int
}

func (b *messageBuffer) add(m message) {
	b.next++
	m.ID = b.next
	size := messageBytes(m)
	for len(b.Items) > 0 && (len(b.Items) >= maxMessages || b.Bytes+size > maxCacheBytes) {
		b.Bytes -= messageBytes(b.Items[0])
		b.Items[0] = message{}
		b.Items = b.Items[1:]
		b.Evicted++
	}
	b.Items = append(b.Items, m)
	b.Bytes += size
	b.counts = nil
}

func (b *messageBuffer) count(topic string) int {
	if topic == "" {
		return len(b.Items)
	}
	if count, ok := b.counts[topic]; ok {
		return count
	}
	count := 0
	for _, message := range b.Items {
		if topicMatches(topic, message.Topic) {
			count++
		}
	}
	if b.counts == nil {
		b.counts = make(map[string]int)
	}
	b.counts[topic] = count
	return count
}
