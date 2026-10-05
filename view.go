package main

import (
	"github.com/egoist/mygo/ui"
	"strings"
)

func (a *app) view(c *ui.Context) {
	a.drain()
	t := ui.LightTheme()
	if a.dark {
		t = ui.DarkTheme()
	} else {
		t.Background = ui.Hex("#ffffff")
		t.Surface = ui.Hex("#f5f7fa")
		t.Border = ui.Hex("#e5eaf0")
		t.Text = ui.Hex("#172b3a")
		t.TextMuted = ui.Hex("#7a8998")
	}
	t.Accent = ui.Hex("#0d9488")
	t.AccentHover = ui.Hex("#0f766e")
	t.AccentPressed = ui.Hex("#115e59")
	t.FontSize = 14
	t.Radius = 8
	c.SetTheme(t)
	if !a.editing && !a.anyDialogOpen() && c.Shortcut(ui.Cmd, ui.KeyEnter) {
		a.publish()
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(54).Shrink(0).Padding(12, 22).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Icon(c, signalIcon).Size(20, 20).TextColor(t.Accent)
			ui.Text(c, "MQTT Desktop").FontSize(15).FontWeight(500)
		})
		ui.Row(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			a.sidebar(c)
			ui.Column(c).Grow(1).MinWidth(0).FillHeight().Children(func() {
				if a.editing {
					a.connectionEditor(c)
				} else if a.current() == nil {
					a.welcome(c)
				} else {
					a.workspace(c)
				}
			})
		})
		ui.Row(c).Height(28).Shrink(0).Padding(6, 20).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
			ui.Text(c, shortText(a.notice, 110)).FontSize(11).Grow(1).TextColor(t.TextMuted)
			if a.dropped.Load() > 0 || a.historyLost.Load() > 0 {
				ui.Textf(c, "队列丢弃 %d · 历史未写入 %d", a.dropped.Load(), a.historyLost.Load()).FontSize(11).TextColor(t.Warning)
			}
		})
	})
	a.dialogs(c)
	if a.deleting {
		if ui.AlertDialog(c, &a.deleting, "删除连接", "删除保存的连接配置？", "取消", "删除") == 1 {
			a.deleteCurrent()
		}
	}
}

func (a *app) workspace(c *ui.Context) {
	p := a.current()
	t := c.Theme()
	state := a.states[p.ID]
	ui.Row(c).Height(88).Shrink(0).Padding(18, 26).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(6).Children(func() {
			ui.Text(c, p.Name).FontSize(17).FontWeight(500)
			ui.Textf(c, "%s  ·  MQTT %s", p.URL, p.Version).FontSize(12).TextColor(t.TextMuted)
		})
		color := t.TextMuted
		if state.Phase == phaseConnected {
			color = t.Success
		}
		if state.Phase == phaseFailed {
			color = t.Danger
		}
		ui.Text(c, "● "+state.label()).FontSize(12).TextColor(color).Tooltip(state.Detail)
		label := a.connectionButton(p.ID)
		if ui.PrimaryButton(c, label).Label(label + " Broker").Width(96).Height(36).Clicked() {
			a.toggleConnection()
		}
		if ui.Button(c, "⋯").Label("连接选项").Width(34).Border(0, t.Border).Clicked() {
			a.connectionOptionsOpen = true
		}
	})
	if state.Detail != "" && (state.Phase == phaseFailed || state.Phase == phaseDisconnected || state.Phase == phaseReconnecting) {
		ui.Text(c, shortText(state.Detail, 120)).FontSize(12).TextColor(t.Danger).Margin(0, 26, 12, 26).Tooltip(state.Detail)
	}
	a.messagesView(c)
	a.publisherView(c)
}

func (a *app) filteredMessages() []message {
	p := a.current()
	if p == nil {
		return nil
	}
	var source []message
	if a.historyVisible {
		source = a.history
	} else if b := a.buffers[p.ID]; b != nil {
		source = b.Items
	}
	out := make([]message, 0, len(source))
	needle := strings.ToLower(a.filter)
	for _, m := range source {
		if a.direction != "全部" && m.Direction != a.direction {
			continue
		}
		if a.topicFilter != "" && !topicMatches(a.topicFilter, m.Topic) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(m.Topic), needle) && !strings.Contains(strings.ToLower(string(m.Payload)), needle) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (a *app) messagesView(c *ui.Context) {
	t := c.Theme()
	items := a.filteredMessages()
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Padding(0, 26, 18, 26).Gap(14).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			name, topic := "全部消息", "当前连接的所有订阅"
			for _, sub := range a.current().Subscriptions {
				if sub.Topic == a.topicFilter {
					name, topic = subscriptionName(sub), sub.Topic
					break
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, name).FontWeight(500).SingleLine().MinWidth(0)
					ui.Textf(c, "%d", len(items)).FontSize(12).TextColor(t.TextMuted)
				})
				ui.Text(c, topic).FontSize(12).TextColor(t.TextMuted).SingleLine().Tooltip(topic)
			})
			if ui.TextInput(c, &a.filter).Label("搜索消息").Placeholder("搜索消息…").Width(260).Height(34).Changed() {
				a.messageIndex = -1
				a.historyIndex = -1
			}
			if ui.Button(c, "⋯").Label("消息选项").Width(34).Border(0, t.Border).Clicked() {
				a.messageOptionsOpen = true
			}
		})
		if a.historyVisible {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "历史消息").FontSize(12).TextColor(t.TextMuted).Grow(1)
				if ui.Button(c, "更早 100 条").Disabled(a.busy["history"] || len(a.history) >= 1000 || a.historyCursor == 0).Clicked() {
					a.loadHistory(false)
				}
				if ui.Button(c, "返回实时").Clicked() {
					a.historyVisible = false
					a.messageIndex = -1
				}
			})
		}
		if len(items) == 0 {
			ui.Column(c).Grow(1).FillWidth().Center().Gap(10).Children(func() {
				ui.Text(c, "等待消息").FontSize(20).TextColor(t.TextMuted)
				ui.Text(c, "连接 Broker 并订阅 Topic 后，消息将出现在这里。").FontSize(12).TextColor(t.TextMuted)
			})
		} else {
			rows := &a.rows
			selected := &a.messageIndex
			if a.historyVisible {
				rows = &a.historyRows
				selected = &a.historyIndex
			}
			rows.Selected = selected
			rows.FollowEnd = !a.paused && !a.historyVisible
			rows.Key = func(i int) any { return items[i].ID }
			list := ui.List(c, rows, len(items), func(i int) {
				m := items[i]
				color := t.Accent
				muted := t.TextMuted
				if m.Direction == "发送" {
					color = t.TextMuted
				}
				if i == *selected {
					color = t.AccentText
					muted = t.AccentText
				}
				ui.Column(c).FillWidth().Padding(14, 12).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, m.Direction).FontSize(11).TextColor(color)
						ui.Text(c, m.Topic).FontSize(13).Bold().Grow(1)
						ui.Text(c, m.At.Format("15:04:05")).FontSize(11).TextColor(muted)
					})
					preview := m.Payload
					if len(preview) > 512 {
						preview = preview[:512]
					}
					ui.Text(c, shortText(string(preview), 150)).Font("Consolas").FontSize(12).TextColor(muted)
				})
			}).Grow(1).FillWidth()
			if list.Changed() || list.Submitted() {
				if *selected >= 0 && *selected < len(items) {
					a.detailMessage = items[*selected]
					a.detailOpen = true
				}
			}
		}
	})
}

func (a *app) publisherView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Height(226).Shrink(0).FillWidth().Padding(18, 26).Gap(12).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "发送到").FontSize(12).TextColor(t.TextMuted)
			ui.TextInput(c, &a.publishTopic).Label("发布 Topic").Placeholder("Topic").Grow(1).Height(34)
			if iconButton(c, "发送选项", slidersIcon).Tooltip("QoS、Retain、消息格式").Clicked() {
				a.publishOptionsOpen = true
			}
		})
		ui.TextArea(c, &a.payload).Label("发布 Payload").FillWidth().Grow(1).Font("Consolas").FontSize(13).Background(t.Surface).Border(1, t.Border)
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			info := a.format + " · QoS " + a.qos
			if a.retain {
				info += " · Retain"
			}
			ui.Text(c, info).FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			ui.Text(c, "Ctrl / ⌘ + Enter").FontSize(11).TextColor(t.TextMuted)
			if ui.PrimaryButton(c, "发送").Label("发送消息").Width(90).Height(34).Disabled(a.states[a.current().ID].Phase != phaseConnected || a.busy["publish"]).Clicked() {
				a.publish()
			}
		})
	})
}
