package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"strconv"
)

func (a *app) welcome(c *ui.Context) {
	ui.Column(c).Fill().Center().Gap(18).Children(func() {
		ui.Text(c, "开始一次 MQTT 调试").FontSize(26).Bold()
		ui.Text(c, "创建连接，订阅主题，实时查看并发布消息。").TextColor(c.Theme().TextMuted)
		if ui.PrimaryButton(c, "创建第一个连接").Clicked() {
			a.edit(newProfile())
		}
	})
}
func field(c *ui.Context, label string, value *string, secret bool) {
	ui.Column(c).FillWidth().Gap(5).Shrink(0).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
		e := ui.TextInput(c, value).Label(label).FillWidth()
		if secret {
			e.Password()
		}
	})
}
func numberField(c *ui.Context, label string, value *int) {
	text := strconv.Itoa(*value)
	ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
		if ui.TextInput(c, &text).Label(label).FillWidth().Changed() {
			if n, err := strconv.Atoi(text); err == nil {
				*value = n
			}
		}
	})
}
func (a *app) connectionEditor(c *ui.Context) {
	p := &a.draft
	t := c.Theme()
	connected := a.sessions[p.ID] != nil
	ui.Column(c).Fill().Padding(24).Gap(18).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "连接配置").FontSize(22).Bold().Grow(1)
			if ui.Button(c, "取消").Clicked() {
				a.editing = false
			}
			if ui.PrimaryButton(c, "保存连接").Disabled(connected || a.busy["save"]).Clicked() {
				a.saveDraft()
			}
		})
		if connected {
			ui.Text(c, "请先断开连接再修改配置。").TextColor(t.Warning)
		}
		ui.Row(c).Gap(8).Children(func() {
			for _, tab := range []string{"基本", "TLS 证书", "会话与遗嘱"} {
				if ui.Button(c, tab).Clicked() {
					a.editTab = tab
				}
			}
		})
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).MaxWidth(760).FillWidth().Gap(16).Children(func() {
				switch a.editTab {
				case "基本":
					field(c, "连接名称", &p.Name, false)
					field(c, "Broker 地址", &p.URL, false)
					ui.Text(c, "mqtt://host:1883 · mqtts://host:8883 · ws://host:8083/mqtt · wss://host:8084/mqtt").FontSize(11).TextColor(t.TextMuted)
					field(c, "Client ID", &p.ClientID, false)
					ui.Row(c).Gap(12).Children(func() { ui.Text(c, "MQTT 版本"); ui.Select(c, &p.Version, []string{"3.1.1", "5.0"}).Width(140) })
					field(c, "用户名", &p.Username, false)
					field(c, "密码", &p.Password, true)
					ui.Row(c).Gap(16).Children(func() {
						numberField(c, "Keep Alive（秒）", &p.KeepAlive)
						numberField(c, "连接超时（秒）", &p.Timeout)
					})
					ui.Checkbox(c, &p.Reconnect, "连接中断后自动重连")
					ui.Checkbox(c, &p.History, "保存消息历史（全局最多 10,000 条，按页读取）")
				case "TLS 证书":
					ui.Text(c, "mqtts / wss 使用系统 CA 验证；也可选择自签名 CA 或客户端证书。").TextColor(t.TextMuted)
					a.fileField(c, "CA 证书（PEM）", &p.CA)
					a.fileField(c, "客户端证书（PEM）", &p.Cert)
					a.fileField(c, "客户端私钥（PEM）", &p.Key)
				case "会话与遗嘱":
					ui.Checkbox(c, &p.Clean, "Clean Session / 首次连接 Clean Start")
					if p.Version == "5.0" {
						numberField(c, "Session Expiry Interval（秒）", &p.SessionExpiry)
					}
					field(c, "遗嘱 Topic（留空关闭）", &p.WillTopic, false)
					ui.Text(c, "遗嘱 Payload").FontSize(12)
					ui.TextArea(c, &p.WillPayload).Height(140).FillWidth().Label("遗嘱 Payload")
					q := strconv.Itoa(int(p.WillQoS))
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "遗嘱 QoS")
						if ui.Select(c, &q, []string{"0", "1", "2"}).Changed() {
							p.WillQoS = byte(q[0] - '0')
						}
						ui.Checkbox(c, &p.WillRetain, "遗嘱 Retain")
					})
				}
			})
		})
	})
}
func (a *app) fileField(c *ui.Context, label string, value *string) {
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, label).FontSize(12)
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, value).Label(label).Grow(1)
			if ui.Button(c, "选择文件").Key(label).Clicked() {
				a.async("file", func() error {
					paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: label})
					if err == nil && len(paths) > 0 {
						path := paths[0]
						a.update(func() { *value = path })
					}
					return err
				}, nil)
			}
		})
	})
}
func (a *app) deleteCurrent() {
	p := a.current()
	if p == nil {
		return
	}
	id := p.ID
	a.async("delete", func() error { return a.store.remove(id) }, func() {
		if s := a.sessions[id]; s != nil {
			delete(a.sessions, id)
			go s.close()
		}
		for i := range a.profiles {
			if a.profiles[i].ID == id {
				a.profiles = append(a.profiles[:i], a.profiles[i+1:]...)
				break
			}
		}
		delete(a.buffers, id)
		delete(a.states, id)
		delete(a.expandedConnections, id)
		a.selected = -1
		a.selectProfile(0)
		a.notice = "连接已删除"
	})
}
