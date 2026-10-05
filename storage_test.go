package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionPersistence(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	p := newProfile()
	p.Password = "local-test-password"
	p.Subscriptions = []subscription{{Topic: "test/#", QoS: 1, Alias: "持久化测试"}}
	defer s.remove(p.ID)
	if err = s.save(p); err != nil {
		t.Fatal(err)
	}
	profiles, err := s.profiles()
	if err != nil || len(profiles) != 1 || profiles[0].Password != p.Password || len(profiles[0].Subscriptions) != 1 {
		t.Fatalf("reload failed: %v", err)
	}
	var data string
	if err = s.db.QueryRow("SELECT data FROM profiles WHERE id=?", p.ID).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, p.Password) {
		t.Fatal("plaintext password written to database")
	}
	exported, err := json.Marshal(profiles)
	if err != nil || strings.Contains(string(exported), p.Password) {
		t.Fatal("password exported")
	}
	if err = s.remove(p.ID); err != nil {
		t.Fatal(err)
	}
	profiles, err = s.profiles()
	if err != nil || len(profiles) != 0 {
		t.Fatal("delete failed")
	}
}
