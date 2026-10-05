package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type app struct {
	store                                                                       *store
	win                                                                         *mygo.Window
	profiles                                                                    []profile
	selected                                                                    int
	expandedConnections                                                         map[string]bool
	sessions                                                                    map[string]*mqttSession
	states                                                                      map[string]connectionState
	buffers                                                                     map[string]*messageBuffer
	inbox                                                                       chan message
	dropped                                                                     atomic.Int64
	inboxBytes                                                                  atomic.Int64
	historyWrites                                                               chan []message
	historyLost                                                                 atomic.Int64
	historyBytes                                                                atomic.Int64
	draft                                                                       profile
	editing, deleting                                                           bool
	editTab                                                                     string
	notice                                                                      string
	dark                                                                        bool
	filter, direction, topicFilter                                              string
	publishTopic, payload, format, qos                                          string
	retain, paused                                                              bool
	subTopic, subAlias, subQoS, subColor                                        string
	rows                                                                        ui.ListState
	messageIndex                                                                int
	detailFormat                                                                string
	detailID                                                                    int64
	detailConnection, detailEncoding, detailText                                string
	history                                                                     []message
	historyCursor                                                               int64
	historyVisible                                                              bool
	historyRows                                                                 ui.ListState
	historyIndex                                                                int
	busy                                                                        map[string]bool
	updates                                                                     chan func()
	subscriptionOpen, subscriptionAdvanced                                      bool
	subscriptionOriginal                                                        string
	connectionOptionsOpen, messageOptionsOpen, publishOptionsOpen, settingsOpen bool
	detailOpen                                                                  bool
	detailMessage                                                               message
}

func newApp(s *store, profiles []profile) *app {
	a := &app{store: s, profiles: profiles, selected: 0, expandedConnections: map[string]bool{}, sessions: map[string]*mqttSession{}, states: map[string]connectionState{}, buffers: map[string]*messageBuffer{}, inbox: make(chan message, 256), historyWrites: make(chan []message, 8), updates: make(chan func(), 128), busy: map[string]bool{}, format: "JSON", qos: "0", subQoS: "0", subColor: "蓝色", direction: "全部", detailFormat: "JSON", messageIndex: -1, historyIndex: -1, editTab: "基本", publishTopic: "test/mygo", payload: "{\n  \"hello\": \"MyGO\"\n}"}
	if len(profiles) > 0 {
		a.expandedConnections[profiles[0].ID] = true
	}
	return a
}
func (a *app) current() *profile {
	if a.selected < 0 || a.selected >= len(a.profiles) {
		return nil
	}
	return &a.profiles[a.selected]
}

func (a *app) selectProfile(index int) {
	if index < 0 || index >= len(a.profiles) {
		return
	}
	if a.selected != index || a.editing {
		a.topicFilter = ""
		a.messageIndex = -1
		a.historyIndex = -1
		a.history = nil
		a.historyCursor = 0
		a.historyVisible = false
		a.detailOpen = false
		a.detailID = 0
		a.rows = ui.ListState{}
		a.historyRows = ui.ListState{}
	}
	a.selected = index
	a.editing = false
	a.expandedConnections[a.profiles[index].ID] = true
}

func (a *app) selectTopic(index int, topic string) {
	a.selectProfile(index)
	if a.topicFilter != topic {
		a.topicFilter = topic
		a.messageIndex = -1
		a.historyIndex = -1
		a.rows = ui.ListState{}
		a.historyRows = ui.ListState{}
	}
}
func (a *app) update(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
	} else {
		a.updates <- fn
	}
}
func (a *app) async(key string, job func() error, done func()) {
	if a.busy[key] {
		return
	}
	a.busy[key] = true
	go func() {
		err := job()
		a.update(func() {
			delete(a.busy, key)
			if err != nil {
				a.notice = err.Error()
			} else if done != nil {
				done()
			}
		})
	}()
}
func (a *app) receive(m message) {
	size := int64(messageBytes(m))
	if !reserveBytes(&a.inboxBytes, size) {
		a.dropped.Add(1)
		return
	}
	select {
	case a.inbox <- m:
	default:
		a.inboxBytes.Add(-size)
		a.dropped.Add(1)
	}
}
func (a *app) drain() {
	for i := 0; i < 128; i++ {
		select {
		case fn := <-a.updates:
			fn()
		default:
			i = 128
		}
	}
	var history []message
	for i := 0; i < 256; i++ {
		select {
		case m := <-a.inbox:
			a.inboxBytes.Add(-int64(messageBytes(m)))
			b := a.buffers[m.Connection]
			if b == nil {
				b = &messageBuffer{}
				a.buffers[m.Connection] = b
			}
			b.add(m)
			for _, p := range a.profiles {
				if p.ID == m.Connection && p.History {
					history = append(history, m)
					break
				}
			}
		default:
			i = 256
		}
	}
	if len(history) > 0 {
		var size int64
		for _, m := range history {
			size += int64(messageBytes(m))
		}
		if !reserveBytes(&a.historyBytes, size) {
			a.historyLost.Add(int64(len(history)))
			return
		}
		select {
		case a.historyWrites <- history:
		default:
			a.historyBytes.Add(-size)
			a.historyLost.Add(int64(len(history)))
		}
	}
}
func (a *app) toggleConnection() {
	p := a.current()
	if p == nil {
		return
	}
	id := p.ID
	if existing := a.sessions[id]; existing != nil {
		delete(a.sessions, id)
		a.states[id] = connectionState{Phase: phaseDisconnected}
		go existing.close()
		return
	}
	cfg := *p
	cfg.Subscriptions = append([]subscription(nil), p.Subscriptions...)
	s := &mqttSession{p: cfg, receive: a.receive}
	s.state = func(state connectionState) {
		a.update(func() {
			if a.sessions[id] == s {
				a.states[id] = state
				if state.Phase == phaseFailed || state.Phase == phaseDisconnected {
					delete(a.sessions, id)
					go s.close()
				}
			}
		})
	}
	s.warning = func(err error) {
		a.update(func() {
			if a.sessions[id] == s {
				a.notice = err.Error()
			}
		})
	}
	a.sessions[id] = s
	a.states[id] = connectionState{Phase: phaseConnecting}
	go func() {
		err := s.connect()
		a.update(func() {
			if err != nil && a.sessions[id] == s {
				a.states[id] = connectionState{Phase: phaseFailed, Detail: err.Error()}
				a.notice = err.Error()
				delete(a.sessions, id)
			}
		})
		if err != nil {
			s.close()
		}
	}()
}
func (a *app) publish() {
	p := a.current()
	if p == nil {
		return
	}
	s := a.sessions[p.ID]
	if s == nil || a.states[p.ID].Phase != phaseConnected {
		a.notice = "请先连接 Broker"
		return
	}
	payload, err := decodePayload(a.payload, a.format)
	if err != nil {
		a.notice = err.Error()
		return
	}
	topic := a.publishTopic
	if err = validTopic(topic, false); err != nil {
		a.notice = err.Error()
		return
	}
	qos := byte(a.qos[0] - '0')
	retain := a.retain
	id := p.ID
	a.async("publish", func() error { return s.publish(topic, payload, qos, retain) }, func() {
		a.receive(message{Connection: id, At: time.Now(), Direction: "发送", Topic: topic, QoS: qos, Retain: retain, Payload: payload, OriginalBytes: len(payload)})
		a.notice = "消息已发送"
	})
}
func (a *app) edit(p profile) {
	p.Subscriptions = append([]subscription(nil), p.Subscriptions...)
	a.draft = p
	a.editing = true
	a.editTab = "基本"
	a.notice = ""
}
func (a *app) saveDraft() {
	p := a.draft
	if err := p.validate(); err != nil {
		a.notice = err.Error()
		return
	}
	a.async("save", func() error { return a.store.save(p) }, func() {
		at := -1
		for i := range a.profiles {
			if a.profiles[i].ID == p.ID {
				at = i
				break
			}
		}
		if at < 0 {
			a.profiles = append(a.profiles, p)
			at = len(a.profiles) - 1
		} else {
			a.profiles[at] = p
		}
		a.selectProfile(at)
		a.notice = "连接配置已保存"
	})
}
func (a *app) changeSubscription(sub subscription, remove bool) {
	p := a.current()
	if p == nil {
		return
	}
	if err := validTopic(sub.Topic, true); err != nil {
		a.notice = err.Error()
		return
	}
	cfg := *p
	cfg.Subscriptions = append([]subscription(nil), p.Subscriptions...)
	s := a.sessions[p.ID]
	a.async("subscription", func() error {
		if s != nil {
			if err := s.subscribe(sub, remove); err != nil {
				return err
			}
		}
		var out []subscription
		for _, v := range cfg.Subscriptions {
			if v.Topic != sub.Topic {
				out = append(out, v)
			}
		}
		if !remove {
			out = append(out, sub)
		}
		cfg.Subscriptions = out
		return a.store.save(cfg)
	}, func() {
		for i := range a.profiles {
			if a.profiles[i].ID == cfg.ID {
				a.profiles[i] = cfg
			}
		}
		if remove && a.current() != nil && a.current().ID == cfg.ID && a.topicFilter == sub.Topic {
			a.selectTopic(a.selected, "")
		}
		if buffer := a.buffers[cfg.ID]; buffer != nil {
			buffer.counts = nil
		}
		a.subTopic = ""
		a.subAlias = ""
		a.subscriptionOpen = false
		a.notice = "订阅配置已保存"
	})
}

func (a *app) connectionButton(id string) string {
	switch a.states[id].Phase {
	case phaseConnecting:
		return "取消连接"
	case phaseReconnecting:
		return "取消重连"
	case phaseConnected:
		return "断开"
	default:
		return "连接"
	}
}
func (a *app) export(messages bool) {
	var data []byte
	var err error
	filename := "connections.json"
	if messages {
		p := a.current()
		if p == nil {
			return
		}
		b := a.buffers[p.ID]
		if b == nil {
			a.notice = "暂无消息"
			return
		}
		data, err = json.MarshalIndent(b.Items, "", "  ")
		filename = "messages.json"
	} else {
		data, err = json.MarshalIndent(a.profiles, "", "  ")
	}
	if err != nil {
		a.notice = err.Error()
		return
	}
	a.async("export", func() error {
		path, e := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "导出 JSON", DefaultPath: filename, Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
		if e != nil || path == "" {
			return e
		}
		return os.WriteFile(path, data, 0600)
	}, func() { a.notice = "导出操作完成；连接密码保存在系统凭据中，不写入文件" })
}
func (a *app) loadHistory(reset bool) {
	p := a.current()
	if p == nil {
		return
	}
	id := p.ID
	cursor := a.historyCursor
	if reset {
		cursor = 0
	}
	var items []message
	var next int64
	a.async("history", func() error { var err error; items, next, err = a.store.history(id, cursor); return err }, func() {
		if a.current() == nil || a.current().ID != id {
			return
		}
		if reset {
			a.history = nil
		}
		a.history = append(a.history, items...)
		bytes := 0
		for _, m := range a.history {
			bytes += messageBytes(m)
		}
		for len(a.history) > 0 && bytes > maxCacheBytes {
			bytes -= messageBytes(a.history[0])
			a.history[0] = message{}
			a.history = a.history[1:]
		}
		a.historyCursor = next
		a.historyVisible = true
		a.historyIndex = -1
		if len(items) == 0 {
			a.notice = "没有更早的历史消息"
		}
	})
}
func main() {
	screenshot := flag.String("screenshot", "", "render a local demo screenshot without opening a window")
	capture := flag.String("native-screenshot", "", "capture the native window and exit (startup check)")
	dataDir := flag.String("data-dir", "", "override the local data directory")
	flag.Parse()
	if *screenshot != "" {
		a := demoApp()
		img := ui.Render(a.view, 1440, 940, 1)
		f, err := os.Create(*screenshot)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err = png.Encode(f, img); err != nil {
			log.Fatal(err)
		}
		return
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	if *dataDir != "" {
		dir = *dataDir
	} else {
		dir = filepath.Join(dir, "mqtt-desktop")
	}
	s, err := openStore(filepath.Join(dir, "data.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer s.db.Close()
	profiles, err := s.profiles()
	if err != nil {
		log.Fatal(err)
	}
	a := newApp(s, profiles)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var writer sync.WaitGroup
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{Title: "MQTT Desktop", Width: 1440, Height: 940, MinWidth: 1100, MinHeight: 740, StateKey: "main", Content: ui.View(a.view)})
		if *capture != "" {
			go func() {
				time.Sleep(3 * time.Second)
				data, e := a.win.CapturePage()
				if e == nil {
					e = os.WriteFile(*capture, data, 0600)
				}
				if e != nil {
					log.Print(e)
				}
				mygo.App.Quit()
			}()
		}
		go func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if len(a.inbox) > 0 {
						a.win.Invalidate()
					}
				}
			}
		}()
		writer.Add(1)
		go func() {
			defer writer.Done()
			for items := range a.historyWrites {
				if e := s.appendMessages(items); e != nil {
					a.update(func() { a.notice = "历史保存失败：" + e.Error() })
				}
				var size int64
				for _, m := range items {
					size += int64(messageBytes(m))
				}
				a.historyBytes.Add(-size)
			}
		}()
	})
	mygo.App.OnQuit(func() {
		cancel()
		for _, session := range a.sessions {
			session.close()
		}
		a.drain()
		close(a.historyWrites)
		writer.Wait()
	})
	if err = mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
func demoApp() *app {
	p := newProfile()
	p.Name = "本地开发"
	p.URL = "mqtt://127.0.0.1:1883"
	p.Version = "5.0"
	p.Subscriptions = []subscription{{Topic: "devices/+/state", Alias: "设备状态", Color: "#38bdf8", QoS: 1}, {Topic: "devices/+/telemetry", Alias: "遥测数据", Color: "#a78bfa", QoS: 0}}
	test := newProfile()
	test.Name = "测试环境"
	test.URL = "mqtt://192.168.1.20:1883"
	test.Subscriptions = []subscription{{Topic: "devices/+/alarm", Alias: "告警事件"}}
	cloud := newProfile()
	cloud.Name = "云端调试"
	cloud.URL = "mqtts://broker.example.com:8883"
	a := newApp(nil, []profile{p, test, cloud})
	a.states[p.ID] = connectionState{Phase: phaseDisconnected}
	a.states[cloud.ID] = connectionState{Phase: phaseFailed, Detail: "演示数据：连接未建立"}
	a.notice = "本地演示数据"
	b := &messageBuffer{}
	a.buffers[p.ID] = b
	for i := 0; i < 25; i++ {
		topic := "devices/sensor-01/telemetry"
		payload := []byte(fmt.Sprintf(`{"temperature":%.1f,"humidity":48,"online":true}`, 23.1+float64(i)*0.1))
		direction := "接收"
		if i%5 == 0 {
			direction = "发送"
			topic = "devices/sensor-01/state"
			payload = []byte(`{"power":"on"}`)
		}
		b.add(message{Connection: p.ID, At: time.Now().Add(time.Duration(i-25) * time.Second), Direction: direction, Topic: topic, QoS: 1, Payload: payload, OriginalBytes: len(payload)})
	}
	a.messageIndex = -1
	return a
}

func shortText(s string, n int) string {
	r := []rune(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func reserveBytes(counter *atomic.Int64, size int64) bool {
	for {
		old := counter.Load()
		if old+size > maxQueueBytes {
			return false
		}
		if counter.CompareAndSwap(old, old+size) {
			return true
		}
	}
}
