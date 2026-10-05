package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

func TestPayloadFormats(t *testing.T) {
	for _, format := range []string{"Hex", "Base64"} {
		raw := []byte{0, 255, 13, 10, 42}
		text := displayPayload(raw, format)
		decoded, err := decodePayload(text, format)
		if err != nil || !bytes.Equal(decoded, raw) {
			t.Fatalf("%s: %v", format, err)
		}
	}
	if _, err := decodePayload("{bad}", "JSON"); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if _, err := decodePayload(string(make([]byte, maxPayloadBytes+1)), "Text"); err == nil {
		t.Fatal("oversized payload accepted")
	}
}
func TestTopicFilters(t *testing.T) {
	for _, test := range []struct {
		filter, topic string
		match         bool
	}{{"a/+/c", "a/b/c", true}, {"a/#", "a", true}, {"#", "$SYS/status", false}, {"$SYS/#", "$SYS/status", true}, {"a/+", "a/b/c", false}, {"$share/workers/a/+", "a/b", true}} {
		if got := topicMatches(test.filter, test.topic); got != test.match {
			t.Errorf("%s %s: %v", test.filter, test.topic, got)
		}
	}
	for _, topic := range []string{"a/#/b", "a+", "a/#x"} {
		if validTopic(topic, true) == nil {
			t.Errorf("accepted %s", topic)
		}
	}
}
func TestMessageBufferBounds(t *testing.T) {
	b := &messageBuffer{}
	for i := 0; i < 3000; i++ {
		b.add(message{Payload: make([]byte, 64), Topic: "test"})
	}
	if len(b.Items) != maxMessages || b.Evicted != 1000 {
		t.Fatalf("count: %d evicted: %d", len(b.Items), b.Evicted)
	}
	for i := 0; i < 40; i++ {
		b.add(message{Payload: make([]byte, maxPayloadBytes)})
	}
	if b.Bytes > maxCacheBytes {
		t.Fatalf("bytes exceeded: %d", b.Bytes)
	}
	for _, m := range b.Items {
		if len(m.Payload) != maxPayloadBytes {
			t.Fatal("old messages not evicted")
		}
	}
}

func TestSubscriptionCountsFollowCacheEviction(t *testing.T) {
	b := &messageBuffer{}
	b.add(message{Topic: "devices/one/state"})
	if b.count("devices/+/state") != 1 || b.count("devices/+/alarm") != 0 {
		t.Fatal("subscription counts did not match wildcard topics")
	}
	b.add(message{Topic: "devices/two/state"})
	if b.count("devices/+/state") != 2 {
		t.Fatal("new messages did not invalidate cached counts")
	}
	for i := 0; i < maxMessages; i++ {
		b.add(message{Topic: "devices/three/alarm"})
	}
	if b.count("devices/+/state") != 0 || b.count("devices/+/alarm") != maxMessages || b.count("") != maxMessages {
		t.Fatal("evicted messages still contribute to subscription counts")
	}
}
func TestHistoryPaginationAndRetention(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	var batch []message
	for i := 0; i < 10020; i++ {
		batch = append(batch, message{Connection: "test", At: time.Unix(int64(i), 0), Topic: "history", Payload: []byte("message")})
	}
	if err = s.appendMessages(batch); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 10000 {
		t.Fatalf("count %d err %v", count, err)
	}
	page, cursor, err := s.history("test", 0)
	if err != nil || len(page) != 100 || page[0].At.Unix() != 10019 {
		t.Fatalf("page err %v count %d", err, len(page))
	}
	next, _, err := s.history("test", cursor)
	if err != nil || len(next) != 100 || next[0].At.Unix() != 9919 {
		t.Fatalf("next page err %v", err)
	}
	if page[99].ID <= next[0].ID {
		t.Fatal("duplicate pagination")
	}
}
func TestInboxIsBounded(t *testing.T) {
	a := newApp(nil, nil)
	for i := 0; i < 1000; i++ {
		a.receive(message{Connection: "test"})
	}
	if len(a.inbox) != 256 || a.dropped.Load() != 744 {
		t.Fatal("unbounded inbox")
	}
	a.drain()
	if len(a.buffers["test"].Items) != 256 {
		t.Fatal("inbox not drained")
	}
}

func TestInboxByteLimit(t *testing.T) {
	a := newApp(nil, nil)
	for i := 0; i < 30; i++ {
		a.receive(message{Connection: "test", Payload: make([]byte, maxPayloadBytes)})
	}
	if a.inboxBytes.Load() > maxQueueBytes || a.dropped.Load() == 0 {
		t.Fatal("byte limit not enforced")
	}
	a.drain()
	if a.inboxBytes.Load() != 0 {
		t.Fatal("bytes not released")
	}
}
