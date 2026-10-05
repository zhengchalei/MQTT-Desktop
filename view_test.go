package main

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitUI(t *testing.T, tt *ui.Tester, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("UI operation timed out")
}
func typeField(t *testing.T, tt *ui.Tester, label, text string) {
	t.Helper()
	click(t, tt, label)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(text)
}
func TestNativeBrokerWorkflow(t *testing.T) {
	address, _, _ := startBroker(t, "mqtt", false)
	p := newProfile()
	p.Name = "本地 Broker · 实际收发验证"
	p.URL = address
	p.Version = "5.0"
	p.History = true
	s, err := openStore(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	data, _ := json.Marshal(p)
	if _, err = s.db.Exec("INSERT INTO profiles(id,data) VALUES(?,?)", p.ID, string(data)); err != nil {
		t.Fatal(err)
	}
	a := newApp(s, []profile{p})
	tt := ui.NewTester(a.view, 1440, 940)
	click(t, tt, "连接 Broker")
	waitUI(t, tt, func() bool { return a.states[p.ID].Phase == phaseConnected })
	session := a.sessions[p.ID]
	defer session.close()
	click(t, tt, "添加订阅 "+p.Name)
	typeField(t, tt, "订阅 Topic 输入", "test/+/state")
	click(t, tt, "高级选项")
	typeField(t, tt, "显示别名", "真实消息验证")
	click(t, tt, "确认订阅")
	waitUI(t, tt, func() bool { return len(a.profiles[0].Subscriptions) == 1 })
	typeField(t, tt, "发布 Topic", "test/device/state")
	typeField(t, tt, "发布 Payload", `{"online":true,"source":"local-broker"}`)
	click(t, tt, "发送消息")
	waitUI(t, tt, func() bool { return len(a.filteredMessages()) == 2 })
	seen := map[string]bool{}
	for _, m := range a.filteredMessages() {
		seen[m.Direction] = true
	}
	if !seen["接收"] || !seen["发送"] {
		t.Fatal("missing send or receive")
	}
	select {
	case batch := <-a.historyWrites:
		if err = s.appendMessages(batch); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("history not queued")
	}
	click(t, tt, "消息选项")
	click(t, tt, "查看历史")
	waitUI(t, tt, func() bool { return a.historyVisible })
	if len(a.history) == 0 {
		t.Fatal("history not loaded")
	}
	click(t, tt, "返回实时")
	if evidence := os.Getenv("MQTT_DESKTOP_EVIDENCE_DIR"); evidence != "" {
		if err = os.MkdirAll(evidence, 0755); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(evidence, "local-broker-roundtrip.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, tt.Image())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	click(t, tt, "断开 Broker")
	if a.sessions[p.ID] != nil {
		t.Fatal("disconnect failed")
	}
}

func click(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("%v; visible labels: %v", err, tt.Texts())
	}
}
func TestNativeConnectionEditor(t *testing.T) {
	a := newApp(nil, nil)
	tt := ui.NewTester(a.view, 1440, 940)
	click(t, tt, "创建第一个连接")
	if !a.editing || !tt.HasText("Broker 地址") {
		t.Fatal("editor not visible")
	}
	click(t, tt, "TLS 证书")
	if !tt.HasText("客户端私钥（PEM）") {
		t.Fatal("TLS fields missing")
	}
	click(t, tt, "会话与遗嘱")
	if !tt.HasText("遗嘱 Payload") {
		t.Fatal("will fields missing")
	}
	click(t, tt, "取消")
	if a.editing {
		t.Fatal("cancel did not close editor")
	}
}
func TestNativeMessageActions(t *testing.T) {
	a := demoApp()
	tt := ui.NewTester(a.view, 1440, 940)
	click(t, tt, "devices/sensor-01/telemetry")
	click(t, tt, "复制 Payload")
	if tt.Clipboard() == "" {
		t.Fatal("copy failed")
	}
	click(t, tt, "填入发布区")
	if a.format != "Base64" || a.publishTopic != "devices/sensor-01/telemetry" {
		t.Fatal("replay failed")
	}
	click(t, tt, "设备状态")
	if a.topicFilter != "devices/+/state" || len(a.filteredMessages()) != 5 {
		t.Fatal("filter failed")
	}
	click(t, tt, "全部消息 "+a.current().Name)
	if len(a.filteredMessages()) != 25 {
		t.Fatal("reset filter failed")
	}
	click(t, tt, "设置")
	click(t, tt, "深色模式")
	if !a.dark {
		t.Fatal("theme did not change")
	}
	click(t, tt, "关闭 设置")
	click(t, tt, "消息选项")
	click(t, tt, "清空消息")
	if len(a.filteredMessages()) != 0 {
		t.Fatal("clear failed")
	}
}

func saveEvidence(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("MQTT_DESKTOP_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = png.Encode(file, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestFailedConnectionUpdatesPromptly(t *testing.T) {
	for _, version := range []string{"3.1.1", "5.0"} {
		t.Run(version, func(t *testing.T) {
			port, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := port.Addr().String()
			port.Close()
			p := newProfile()
			p.URL = "mqtt://" + address
			p.Version = version
			p.Timeout = 10
			p.Reconnect = true
			a := newApp(nil, []profile{p})
			tt := ui.NewTester(a.view, 1440, 940)
			start := time.Now()
			click(t, tt, "连接 Broker")
			if a.states[p.ID].Phase == phaseConnecting && a.connectionButton(p.ID) != "取消连接" {
				t.Fatal("connecting button is wrong")
			}
			waitUI(t, tt, func() bool { return a.states[p.ID].Phase == phaseFailed })
			elapsed := time.Since(start)
			if elapsed > 2*time.Second {
				t.Fatalf("failed connection took %s despite closed local port", elapsed)
			}
			if a.sessions[p.ID] != nil || a.connectionButton(p.ID) != "连接" || tt.HasText("断开") {
				t.Fatal("failed connection retained disconnect UI")
			}
			if a.states[p.ID].Detail == "" {
				t.Fatal("failure detail missing")
			}
			if _, ok := tt.Find("连接 Broker"); !ok {
				t.Fatal("retry action missing")
			}
			before := len(a.inbox)
			click(t, tt, "发送消息")
			if len(a.inbox) != before || a.busy["publish"] {
				t.Fatal("failed connection allowed publishing")
			}
			t.Logf("%s connection refusal -> retry UI: %s", version, elapsed)
			if version == "5.0" {
				saveEvidence(t, tt, "connection-failed.png")
			}
		})
	}
}

func TestConnectionLossUpdatesPromptly(t *testing.T) {
	for _, version := range []string{"3.1.1", "5.0"} {
		for _, reconnect := range []bool{false, true} {
			name := version + "/manual"
			if reconnect {
				name = version + "/reconnect"
			}
			t.Run(name, func(t *testing.T) {
				address, _, _, srv := startBrokerInstance(t, "mqtt", false)
				p := newProfile()
				p.URL = address
				p.Version = version
				p.Reconnect = reconnect
				a := newApp(nil, []profile{p})
				tt := ui.NewTester(a.view, 1440, 940)
				click(t, tt, "连接 Broker")
				waitUI(t, tt, func() bool { return a.states[p.ID].Phase == phaseConnected })
				session := a.sessions[p.ID]
				defer session.close()
				start := time.Now()
				client, ok := srv.Clients.Get(p.ClientID)
				if !ok {
					t.Fatal("connected client missing")
				}
				srv.Listeners.Close("tcp", func(string) {})
				client.Net.Conn.Close()
				waitUI(t, tt, func() bool { return a.states[p.ID].Phase != phaseConnected })
				elapsed := time.Since(start)
				if elapsed > time.Second {
					t.Fatalf("loss detection took %s", elapsed)
				}
				if tt.HasText("断开") {
					t.Fatal("stale disconnect UI")
				}
				if reconnect {
					if a.connectionButton(p.ID) != "取消重连" {
						t.Fatal("reconnect action missing")
					}
					click(t, tt, "取消重连 Broker")
				} else if a.sessions[p.ID] != nil {
					t.Fatal("disconnected session retained")
				}
				t.Logf("loss -> UI update: %s", elapsed)
			})
		}
	}
}

func TestSubscriptionDialogIsSimple(t *testing.T) {
	a := demoApp()
	tt := ui.NewTester(a.view, 1440, 940)
	for _, label := range []string{"确认订阅", "取消订阅", "导出连接", "删除连接", "导出消息", "格式化 JSON", "QoS", "Retain"} {
		if _, ok := tt.Find(label); ok {
			t.Fatalf("advanced control %q remains on main screen", label)
		}
	}
	click(t, tt, "添加订阅 "+a.current().Name)
	if !a.subscriptionOpen {
		t.Fatal("subscription dialog did not open")
	}
	if _, ok := tt.Find("显示别名"); ok {
		t.Fatal("advanced fields visible by default")
	}
	saveEvidence(t, tt, "subscription-dialog.png")
	click(t, tt, "高级选项")
	if _, ok := tt.Find("显示别名"); !ok {
		t.Fatal("advanced fields not accessible")
	}
	tt.Key(0, ui.KeyEscape)
	if a.subscriptionOpen {
		t.Fatal("Escape did not close dialog")
	}
	click(t, tt, "设备状态")
	click(t, tt, "订阅选项 devices/+/state")
	if a.subscriptionOriginal != "devices/+/state" || !tt.HasText("取消订阅") {
		t.Fatal("subscription management missing")
	}
	click(t, tt, "取消")
	click(t, tt, "连接选项")
	if !tt.HasText("编辑连接") || !tt.HasText("删除连接") {
		t.Fatal("connection actions missing")
	}
	click(t, tt, "关闭 连接选项")
	click(t, tt, "发送选项")
	if !tt.HasText("QoS") || !tt.HasText("保留消息（Retain）") {
		t.Fatal("publish options missing")
	}
	click(t, tt, "完成")
}

func TestConnectionTreeNavigation(t *testing.T) {
	a := demoApp()
	first, second := a.profiles[0], a.profiles[1]
	a.buffers[second.ID] = &messageBuffer{}
	a.buffers[second.ID].add(message{Connection: second.ID, Topic: "devices/sensor-02/alarm", Direction: "接收", Payload: []byte(`{"alarm":true}`)})
	tt := ui.NewTester(a.view, 1100, 740)
	click(t, tt, "遥测数据")
	click(t, tt, "收起连接 "+first.Name)
	if a.current().ID != first.ID || a.topicFilter != "devices/+/telemetry" || a.expandedConnections[first.ID] {
		t.Fatal("collapse changed the active connection or topic")
	}
	if _, visible := tt.Find("添加订阅 " + first.Name); visible {
		t.Fatal("collapsed connection still shows subscriptions")
	}
	click(t, tt, "展开连接 "+second.Name)
	if a.current().ID != first.ID || !a.expandedConnections[second.ID] {
		t.Fatal("expanding another connection changed selection")
	}
	a.historyVisible = true
	a.history = []message{{Connection: first.ID, Topic: "devices/old/state"}}
	tt.Frame()
	click(t, tt, "告警事件")
	if a.current().ID != second.ID || a.topicFilter != "devices/+/alarm" || len(a.filteredMessages()) != 1 || a.historyVisible || len(a.history) != 0 {
		t.Fatal("topic selection did not switch its owning connection and clear old history")
	}
	click(t, tt, "展开连接 "+first.Name)
	click(t, tt, "添加订阅 "+first.Name)
	if a.current().ID != first.ID || !a.subscriptionOpen || !a.expandedConnections[first.ID] {
		t.Fatal("subscription dialog belongs to the wrong connection")
	}
	click(t, tt, "取消")
	if err := tt.RightClick("告警事件"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("订阅选项"); err != nil {
		t.Fatal(err)
	}
	if a.current().ID != second.ID || a.subscriptionOriginal != "devices/+/alarm" {
		t.Fatal("context menu edited a subscription on the wrong connection")
	}
	click(t, tt, "取消")
	saveEvidence(t, tt, "connection-tree-selected.png")
}

func TestConnectionTreeSubscriptionOwnership(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	first, second := newProfile(), newProfile()
	first.Name, second.Name = "开发环境", "测试环境"
	first.Subscriptions = []subscription{{Topic: "shared/+/state", Alias: "开发状态"}}
	second.Subscriptions = []subscription{{Topic: "shared/+/state", Alias: "测试状态"}}
	a := newApp(s, []profile{first, second})
	tt := ui.NewTester(a.view, 1440, 940)
	click(t, tt, "展开连接 "+second.Name)
	click(t, tt, "添加订阅 "+second.Name)
	typeField(t, tt, "订阅 Topic 输入", "test/+/alarm")
	click(t, tt, "确认订阅")
	waitUI(t, tt, func() bool { return !a.busy["subscription"] && !a.subscriptionOpen })
	if len(a.profiles[0].Subscriptions) != 1 || len(a.profiles[1].Subscriptions) != 2 {
		t.Fatal("new subscription was saved to the wrong connection")
	}
	click(t, tt, "测试状态")
	click(t, tt, "订阅选项 shared/+/state")
	click(t, tt, "取消订阅")
	waitUI(t, tt, func() bool { return !a.busy["subscription"] && !a.subscriptionOpen })
	if a.current().ID != second.ID || a.topicFilter != "" || len(a.profiles[0].Subscriptions) != 1 || len(a.profiles[1].Subscriptions) != 1 {
		t.Fatal("unsubscribe changed another connection or retained the removed filter")
	}
	stored, err := s.profiles()
	if err != nil || len(stored) != 1 || stored[0].ID != second.ID || stored[0].Subscriptions[0].Topic != "test/+/alarm" {
		t.Fatalf("subscription changes were not persisted: %v", err)
	}
}
