package main

import (
	"github.com/egoist/mygo/ui"
	"strconv"
)

var subscriptionColors = map[string]string{"蓝色": "#38bdf8", "紫色": "#a78bfa", "绿色": "#34d399", "橙色": "#fb923c"}

func (a *app) openSubscription(sub *subscription) {
	a.subscriptionOpen = true
	a.subscriptionAdvanced = false
	a.subscriptionOriginal = ""
	a.subTopic = ""
	a.subAlias = ""
	a.subQoS = "0"
	a.subColor = "蓝色"
	a.notice = ""
	if sub != nil {
		a.subTopic = sub.Topic
		a.subscriptionOriginal = sub.Topic
		a.subAlias = sub.Alias
		a.subQoS = strconv.Itoa(int(sub.QoS))
		a.subscriptionAdvanced = true
		for name, color := range subscriptionColors {
			if color == sub.Color {
				a.subColor = name
			}
		}
	}
}

func (a *app) anyDialogOpen() bool {
	return a.subscriptionOpen || a.connectionOptionsOpen || a.messageOptionsOpen || a.publishOptionsOpen || a.settingsOpen || a.detailOpen || a.deleting
}

func dialog(c *ui.Context, open *bool, title string, width float32, body func()) {
	ui.Modal(c, open, func() {
		ui.Column(c).Width(width).Gap(20).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title).FontSize(20).Bold().Grow(1)
				if ui.Button(c, "×").Label("关闭 "+title).Width(28).Border(0, c.Theme().Border).Clicked() {
					*open = false
				}
			})
			body()
		})
	})
}

func (a *app) dialogs(c *ui.Context) {
	t := c.Theme()
	dialog(c, &a.subscriptionOpen, "订阅设置", 420, func() {
		submit := ui.TextInput(c, &a.subTopic).Label("订阅 Topic 输入").Placeholder("例如 devices/+/state").FillWidth().Disabled(a.subscriptionOriginal != "").Submitted()
		ui.Text(c, "支持 + 和 # 通配符").FontSize(12).TextColor(t.TextMuted)
		ui.Collapsible(c, "高级选项", &a.subscriptionAdvanced, func() {
			ui.Column(c).FillWidth().Gap(12).Children(func() {
				field(c, "显示别名", &a.subAlias, false)
				ui.Row(c).Gap(12).Children(func() { ui.Text(c, "QoS").Grow(1); ui.Select(c, &a.subQoS, []string{"0", "1", "2"}).Width(120) })
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "颜色").Grow(1)
					ui.Select(c, &a.subColor, []string{"蓝色", "紫色", "绿色", "橙色"}).Width(120)
				})
			})
		})
		if a.notice != "" {
			ui.Text(c, shortText(a.notice, 60)).FontSize(12).TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(8).Children(func() {
			if a.subscriptionOriginal != "" {
				if ui.Button(c, "取消订阅").TextColor(t.Danger).Disabled(a.busy["subscription"]).Clicked() {
					a.changeSubscription(subscription{Topic: a.subscriptionOriginal}, true)
				}
			}
			ui.Spacer(c)
			if ui.Button(c, "取消").Clicked() {
				a.subscriptionOpen = false
			}
			label := "订阅"
			if a.subscriptionOriginal != "" {
				label = "保存"
			}
			if (ui.PrimaryButton(c, label).Label("确认订阅").Disabled(a.busy["subscription"]).Clicked() || submit) && !a.busy["subscription"] {
				a.changeSubscription(subscription{Topic: a.subTopic, Alias: a.subAlias, QoS: byte(a.subQoS[0] - '0'), Color: subscriptionColors[a.subColor]}, false)
			}
		})
	})
	dialog(c, &a.connectionOptionsOpen, "连接选项", 360, func() {
		p := a.current()
		if p == nil {
			return
		}
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			if ui.Button(c, "编辑连接").FillWidth().Clicked() {
				a.connectionOptionsOpen = false
				a.edit(*p)
			}
			if ui.Button(c, "复制连接").FillWidth().Clicked() {
				copy := *p
				fresh := newProfile()
				copy.ID = fresh.ID
				copy.ClientID = fresh.ClientID
				copy.Name += " 副本"
				a.connectionOptionsOpen = false
				a.edit(copy)
			}
			if ui.Button(c, "导出连接").FillWidth().Disabled(a.busy["export"]).Clicked() {
				a.connectionOptionsOpen = false
				a.export(false)
			}
			if ui.Button(c, "删除连接").FillWidth().TextColor(t.Danger).Clicked() {
				a.connectionOptionsOpen = false
				a.deleting = true
			}
		})
	})
	dialog(c, &a.messageOptionsOpen, "消息选项", 360, func() {
		ui.Column(c).FillWidth().Gap(14).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "显示方向").Grow(1)
				ui.Select(c, &a.direction, []string{"全部", "接收", "发送"}).Width(130)
			})
			ui.Checkbox(c, &a.paused, "暂停自动滚动")
			if ui.Button(c, "查看历史").FillWidth().Disabled(a.busy["history"]).Clicked() {
				a.messageOptionsOpen = false
				a.loadHistory(true)
			}
			if ui.Button(c, "导出消息").FillWidth().Disabled(a.busy["export"]).Clicked() {
				a.messageOptionsOpen = false
				a.export(true)
			}
			if ui.Button(c, "清空消息").FillWidth().TextColor(t.Danger).Clicked() {
				a.buffers[a.current().ID] = &messageBuffer{}
				a.messageIndex = -1
				a.detailID = 0
				a.detailText = ""
				a.messageOptionsOpen = false
			}
		})
	})
	dialog(c, &a.publishOptionsOpen, "发送选项", 360, func() {
		ui.Column(c).FillWidth().Gap(16).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "格式").Grow(1)
				ui.Select(c, &a.format, []string{"JSON", "Text", "Hex", "Base64"}).Width(140)
			})
			ui.Row(c).Gap(12).Children(func() { ui.Text(c, "QoS").Grow(1); ui.Select(c, &a.qos, []string{"0", "1", "2"}).Width(140) })
			ui.Checkbox(c, &a.retain, "保留消息（Retain）")
			if ui.Button(c, "格式化 JSON").FillWidth().Disabled(a.format != "JSON").Clicked() {
				b, err := decodePayload(a.payload, "JSON")
				if err != nil {
					a.notice = err.Error()
				} else {
					a.payload = displayPayload(b, "JSON")
				}
			}
			if ui.PrimaryButton(c, "完成").FillWidth().Clicked() {
				a.publishOptionsOpen = false
			}
		})
	})
	dialog(c, &a.settingsOpen, "设置", 360, func() {
		ui.Checkbox(c, &a.dark, "深色模式")
		ui.Text(c, "每个连接缓存最多 2,000 条 / 16 MiB").FontSize(12).TextColor(t.TextMuted)
	})
	dialog(c, &a.detailOpen, "消息详情", 680, func() {
		m := a.detailMessage
		ui.Text(c, m.Topic).FontSize(13).Bold()
		ui.Textf(c, "%s · %s · QoS %d · %d B", m.Direction, m.At.Format("15:04:05.000"), m.QoS, m.OriginalBytes).FontSize(12).TextColor(t.TextMuted)
		ui.Select(c, &a.detailFormat, []string{"JSON", "Text", "Hex", "Base64"}).Width(130)
		if a.detailID != m.ID || a.detailConnection != m.Connection || a.detailEncoding != a.detailFormat {
			a.detailID = m.ID
			a.detailConnection = m.Connection
			a.detailEncoding = a.detailFormat
			a.detailText = displayPayload(m.Payload, a.detailFormat)
		}
		if m.Truncated {
			ui.Text(c, "消息超过 1 MiB，仅保留前 1 MiB。").FontSize(12).TextColor(t.Warning)
		}
		ui.ScrollBoth(c).Height(260).FillWidth().Padding(14).Background(t.Surface).Radius(8).Children(func() { ui.Text(c, a.detailText).Font("Consolas").FontSize(13).Selectable() })
		ui.Row(c).Gap(10).Children(func() {
			ui.Spacer(c)
			if ui.Button(c, "复制 Payload").Clicked() {
				c.WriteClipboard(a.detailText)
			}
			if ui.PrimaryButton(c, "填入发布区").Clicked() {
				a.publishTopic = m.Topic
				a.payload = displayPayload(m.Payload, "Base64")
				a.format = "Base64"
				a.qos = strconv.Itoa(int(m.QoS))
				a.retain = m.Retain
				a.detailOpen = false
			}
		})
	})
}
