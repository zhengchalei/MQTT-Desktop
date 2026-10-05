package main

import "github.com/egoist/mygo/ui"

var (
	signalIcon  = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="M12 12v8m-3 0h6M8.5 8.5a5 5 0 0 0 0 7m7-7a5 5 0 0 1 0 7M5 5a10 10 0 0 0 0 14M19 5a10 10 0 0 1 0 14" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/><circle cx="12" cy="9" r="1.5"/></svg>`))
	chevronIcon = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="m9 6 6 6-6 6" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`))
	plusIcon    = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></svg>`))
	inboxIcon   = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="m4 10 3-6h10l3 6v10H4zm0 0h5l1 4h4l1-4h5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"/></svg>`))
	topicIcon   = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="m10 3-4 18m12-18-4 18M4 8h17M3 16h17" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>`))
	slidersIcon = ui.MustParseSVG([]byte(`<svg viewBox="0 0 24 24"><path d="M4 6h4m4 0h8M4 12h10m4 0h2M4 18h2m4 0h10M8 3v6m6 0v6M6 15v6" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>`))
)

func iconButton(c *ui.Context, label string, icon *ui.SVG) *ui.Element {
	t := c.Theme()
	button := ui.ButtonBase(c).Label(label).Role(ui.RoleButton).Size(30, 30).Radius(7).TextColor(t.TextMuted)
	if button.Hovered() {
		button.Background(t.SurfaceHover).TextColor(t.Text)
	}
	button.Children(func() { ui.Icon(c, icon).Size(16, 16) })
	return button
}

func navigationButton(c *ui.Context, label string, selected bool) *ui.Element {
	t := c.Theme()
	button := ui.ButtonBase(c).Label(label).Role(ui.RoleButton).Justify(ui.Start).AlignItems(ui.Center).Height(38).Padding(8, 10).Gap(9).Radius(7)
	if selected {
		button.Background(t.Accent.Mix(t.Background, 0.92)).TextColor(t.Accent)
	} else if button.Hovered() {
		button.Background(t.SurfaceHover)
	}
	return button
}

func connectionColor(c *ui.Context, state connectionState) ui.Color {
	t := c.Theme()
	switch state.Phase {
	case phaseConnected:
		return t.Success
	case phaseFailed:
		return t.Danger
	case phaseConnecting, phaseReconnecting:
		return t.Warning
	default:
		return t.TextMuted
	}
}

func subscriptionName(sub subscription) string {
	if sub.Alias != "" {
		return sub.Alias
	}
	return sub.Topic
}

func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(260).Shrink(0).FillHeight().Padding(18, 12, 12, 12).Gap(12).Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).PaddingX(10).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, "连接").FontSize(12).FontWeight(500).TextColor(t.TextMuted).Grow(1)
			if iconButton(c, "新建连接", plusIcon).Tooltip("新建连接").Clicked() {
				a.edit(newProfile())
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				for i, p := range a.profiles {
					ui.Column(c).Key(p.ID).FillWidth().Shrink(0).Gap(3).Children(func() {
						a.connectionBranch(c, i)
						if a.expandedConnections[p.ID] {
							a.subscriptionBranch(c, i)
						}
					})
				}
			})
		})
		if navigationButton(c, "设置", false).TextColor(t.TextMuted).FillWidth().Children(func() {
			ui.Icon(c, slidersIcon).Size(16, 16)
			ui.Text(c, "设置").FontSize(12)
		}).Clicked() {
			a.settingsOpen = true
		}
	})
}

func (a *app) connectionBranch(c *ui.Context, index int) {
	p := a.profiles[index]
	state := a.states[p.ID]
	t := c.Theme()
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(1).Shrink(0)
	row.Children(func() {
		expanded := a.expandedConnections[p.ID]
		label := "展开连接 " + p.Name
		if expanded {
			label = "收起连接 " + p.Name
		}
		fold := ui.ButtonBase(c).Label(label).Role(ui.RoleButton).Size(22, 30).Radius(7).TextColor(t.TextMuted)
		if fold.Hovered() {
			fold.Background(t.SurfaceHover)
		}
		fold.Children(func() {
			arrow := ui.Icon(c, chevronIcon).Size(14, 14)
			if expanded {
				arrow.Rotate(90)
			}
		})
		// The arrow and name are separate targets: collapsing keeps the session and message selection.
		if fold.Clicked() {
			a.expandedConnections[p.ID] = !expanded
		}
		button := navigationButton(c, p.Name, false).Grow(1).MinWidth(0).Padding(6, 4).Tooltip(p.URL + " · " + state.label())
		if state.Phase == phaseFailed || state.Phase == phaseConnecting || state.Phase == phaseReconnecting {
			button.Height(48)
		}
		button.Children(func() {
			ui.Box(c).Size(7, 7).Shrink(0).Radius(4).Background(connectionColor(c, state))
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				ui.Text(c, p.Name).FontWeight(500).SingleLine()
				if state.Phase == phaseFailed || state.Phase == phaseConnecting || state.Phase == phaseReconnecting {
					ui.Text(c, state.label()).FontSize(11).TextColor(connectionColor(c, state))
				}
			})
		})
		button.ContextMenu(func(menu *ui.Menu) {
			if menu.Item("连接选项").Chosen() {
				a.selectProfile(index)
				a.connectionOptionsOpen = true
			}
		})
		if button.Clicked() {
			a.selectProfile(index)
		}
		if index == a.selected || row.Hovered() || row.FocusWithin() {
			if ui.Button(c, "⋯").Label("连接选项 "+p.Name).Size(28, 30).Padding(0).Border(0, t.Border).Background(t.Surface).TextColor(t.TextMuted).Clicked() {
				a.selectProfile(index)
				a.connectionOptionsOpen = true
			}
		} else {
			ui.Box(c).Size(28, 30).Shrink(0)
		}
	})
}

func (a *app) subscriptionBranch(c *ui.Context, index int) {
	p := a.profiles[index]
	t := c.Theme()
	counts := map[string]int{}
	total := 0
	if buffer := a.buffers[p.ID]; buffer != nil {
		total = len(buffer.Items)
		for _, sub := range p.Subscriptions {
			counts[sub.Topic] = buffer.count(sub.Topic)
		}
	}
	ui.Column(c).AlignSelf(ui.Stretch).Shrink(0).Margin(0, 0, 10, 22).Padding(0, 0, 0, 8).Gap(2).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		all := navigationButton(c, "全部消息 "+p.Name, index == a.selected && !a.editing && a.topicFilter == "").FillWidth()
		all.Children(func() {
			ui.Icon(c, inboxIcon).Size(16, 16)
			ui.Text(c, "全部消息").Grow(1).SingleLine()
			if total > 0 {
				ui.Textf(c, "%d", total).FontSize(12).TextColor(t.TextMuted)
			}
		})
		if all.Clicked() {
			a.selectTopic(index, "")
		}
		for _, sub := range p.Subscriptions {
			selected := index == a.selected && !a.editing && a.topicFilter == sub.Topic
			row := ui.Row(c).Key(sub.Topic).FillWidth().AlignItems(ui.Center).Radius(7).Shrink(0)
			if selected {
				row.Background(t.Accent.Mix(t.Background, 0.92))
			}
			row.Children(func() {
				button := navigationButton(c, subscriptionName(sub), selected).Grow(1).MinWidth(0).Tooltip(sub.Topic)
				button.Children(func() {
					ui.Icon(c, topicIcon).Size(16, 16).TextColor(t.TextMuted)
					ui.Text(c, subscriptionName(sub)).Grow(1).MinWidth(0).SingleLine()
					if count := counts[sub.Topic]; count > 0 {
						ui.Textf(c, "%d", count).FontSize(12).TextColor(t.TextMuted)
					}
				})
				button.ContextMenu(func(menu *ui.Menu) {
					if menu.Item("订阅选项").Chosen() {
						a.selectTopic(index, sub.Topic)
						a.openSubscription(&sub)
					}
				})
				if button.Clicked() {
					a.selectTopic(index, sub.Topic)
				}
				if selected || row.Hovered() || row.FocusWithin() {
					if ui.Button(c, "⋯").Label("订阅选项 "+sub.Topic).Size(28, 30).Padding(0).Border(0, t.Border).Background(ui.Color{}).TextColor(t.TextMuted).Clicked() {
						a.selectTopic(index, sub.Topic)
						a.openSubscription(&sub)
					}
				}
			})
		}
		if navigationButton(c, "添加订阅 "+p.Name, false).FillWidth().TextColor(t.TextMuted).Margin(4, 0, 0, 0).Children(func() {
			ui.Icon(c, plusIcon).Size(16, 16)
			ui.Text(c, "订阅 Topic")
		}).Clicked() {
			a.selectProfile(index)
			a.openSubscription(nil)
		}
	})
}
