package main

// eVPN desktop client (Fyne). Manages multiple tunnel profiles and connects
// through the eVPN carrier. "Full tunnel" routes the whole device via sing-box
// (needs admin/root); otherwise it just runs the local SOCKS5 proxy.

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

type ui struct {
	win      fyne.Window
	profiles []*Profile
	list     *widget.List
	selected int

	svc       *Service
	connected bool

	fullTun    *widget.Check
	connectBtn *widget.Button
	status     *widget.Label
	exitIP     *widget.Label
	logView    *widget.Entry
}

func main() {
	a := app.New()
	a.Settings().SetTheme(forcedDark{})
	w := a.NewWindow("Orbix Tunnel")
	w.Resize(fyne.NewSize(720, 480))

	u := &ui{win: w, selected: -1}
	u.build()
	u.reload()

	u.setupTray(a)
	// Closing the window keeps the app (and the tunnel) alive in the tray.
	// Quit from the tray (or Cmd+Q) tears the tunnel down cleanly.
	w.SetCloseIntercept(func() { w.Hide() })
	a.Lifecycle().SetOnStopped(func() {
		if u.svc != nil {
			u.svc.Stop()
		}
	})

	w.SetContent(u.content())
	w.ShowAndRun()
}

func (u *ui) setupTray(a fyne.App) {
	desk, ok := a.(desktop.App)
	if !ok {
		return
	}
	m := fyne.NewMenu("Orbix Tunnel",
		fyne.NewMenuItem("Show", func() { u.win.Show(); u.win.RequestFocus() }),
		fyne.NewMenuItem("Disconnect", func() {
			if u.connected {
				u.disconnect()
			}
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() { u.quit(a) }),
	)
	desk.SetSystemTrayMenu(m)
}

func (u *ui) quit(a fyne.App) {
	// If the tunnel can't be torn down (admin prompt cancelled), stay open:
	// quitting now would leave sing-box routing everything into a dead proxy.
	if u.svc != nil && !u.svc.Stop() {
		u.log("could not stop the tunnel (admin cancelled?) - not quitting")
		u.win.Show()
		return
	}
	a.Quit()
}

func (u *ui) build() {
	u.list = widget.NewList(
		func() int { return len(u.profiles) },
		func() fyne.CanvasObject { return widget.NewLabel("template") },
		func(id widget.ListItemID, o fyne.CanvasObject) {
			if id >= 0 && id < len(u.profiles) {
				p := u.profiles[id]
				o.(*widget.Label).SetText(fmt.Sprintf("%s  (%s)", p.Name, p.Server))
			}
		},
	)
	u.list.OnSelected = func(id widget.ListItemID) { u.selected = id }
	u.list.OnUnselected = func(id widget.ListItemID) {
		if u.selected == id {
			u.selected = -1
		}
	}

	u.fullTun = widget.NewCheck("Full tunnel (whole device - will ask for admin)", nil)
	u.connectBtn = widget.NewButton("Connect", u.onConnect)
	u.status = widget.NewLabel("disconnected")
	u.exitIP = widget.NewLabel("Exit IP: -")

	u.logView = widget.NewMultiLineEntry()
	u.logView.TextStyle = fyne.TextStyle{Monospace: true}
	u.logView.Wrapping = fyne.TextWrapWord
	u.logView.Disable()
	u.logView.SetMinRowsVisible(8)
}

func (u *ui) content() fyne.CanvasObject {
	newBtn := widget.NewButton("New...", u.onNew)
	importBtn := widget.NewButton("Import...", u.onImport)
	delBtn := widget.NewButton("Delete", u.onDelete)

	left := container.NewBorder(
		widget.NewLabelWithStyle("Profiles", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewGridWithColumns(3, newBtn, importBtn, delBtn),
		nil, nil,
		u.list,
	)

	controls := container.NewVBox(
		u.fullTun,
		u.connectBtn,
		u.status,
		u.exitIP,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Log", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
	)
	// log fills the remaining vertical space (the entry scrolls internally)
	right := container.NewBorder(controls, nil, nil, nil, u.logView)

	split := container.NewHSplit(left, right)
	split.SetOffset(0.4)
	return split
}

func (u *ui) current() *Profile {
	if u.selected >= 0 && u.selected < len(u.profiles) {
		return u.profiles[u.selected]
	}
	return nil
}

func (u *ui) reload() {
	u.profiles = listProfiles()
	if u.selected >= len(u.profiles) {
		u.selected = -1
	}
	if u.list != nil {
		u.list.Refresh()
	}
}

func (u *ui) log(format string, args ...any) {
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...) + "\n"
	u.logView.SetText(u.logView.Text + line)
}

func (u *ui) setStatus(s string) { u.status.SetText(s) }

func (u *ui) onNew() {
	name := widget.NewEntry()
	name.SetPlaceHolder("phone")
	server := widget.NewEntry()
	server.SetPlaceHolder("203.0.113.10")
	port := widget.NewEntry()
	port.SetText("8443")
	token := widget.NewEntry()
	token.SetPlaceHolder("64-hex token from the server")
	tls := widget.NewCheck("TLS (wss)", nil)

	items := []*widget.FormItem{
		{Text: "Name", Widget: name},
		{Text: "Server", Widget: server},
		{Text: "Port", Widget: port},
		{Text: "Token", Widget: token},
		{Text: "", Widget: tls},
	}
	dialog.ShowForm("New profile", "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		p, err := saveProfile(&Profile{
			Name:   name.Text,
			Server: server.Text,
			Port:   port.Text,
			Token:  token.Text,
			TLS:    tls.Checked,
		})
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.reload()
		u.log("created profile %q", p.Name)
	}, u.win)
}

func (u *ui) onImport() {
	d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil || rc == nil {
			return
		}
		path := rc.URI().Path()
		rc.Close()
		p, err := importProfile(path)
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.reload()
		u.log("imported profile %q", p.Name)
	}, u.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".orb", ".evpn"}))
	d.Show()
}

func (u *ui) onDelete() {
	p := u.current()
	if p == nil {
		return
	}
	if u.connected {
		dialog.ShowInformation("Busy", "Disconnect before deleting.", u.win)
		return
	}
	dialog.ShowConfirm("Delete", "Delete profile "+p.Name+"?", func(ok bool) {
		if !ok {
			return
		}
		if err := deleteProfile(p); err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		u.selected = -1
		u.reload()
		u.log("deleted profile %q", p.Name)
	}, u.win)
}

func (u *ui) onConnect() {
	if u.connected {
		u.disconnect()
		return
	}
	p := u.current()
	if p == nil {
		dialog.ShowInformation("No profile", "Select a profile first.", u.win)
		return
	}
	full := u.fullTun.Checked
	u.connectBtn.Disable()
	u.setStatus("connecting...")
	u.log("connecting to %s:%s ...", p.Server, p.Port)

	go func() {
		svc, err := StartService(p)
		if err != nil {
			u.fail("start failed: " + err.Error())
			return
		}
		if full {
			if err := svc.StartTUN(p); err != nil {
				svc.Stop()
				u.fail("TUN failed: " + err.Error())
				return
			}
			u.log("full tunnel up (sing-box)")
		}
		u.svc = svc
		u.connected = true
		u.connectBtn.SetText("Disconnect")
		u.connectBtn.Enable()
		u.setStatus("connected")

		if ip, err := ExitIP(p.Socks); err == nil {
			u.exitIP.SetText("Exit IP: " + ip)
			u.log("exit IP: %s", ip)
		} else {
			u.log("exit IP check failed: %v", err)
		}
	}()
}

func (u *ui) disconnect() {
	if u.svc != nil {
		if !u.svc.Stop() {
			u.log("could not stop the tunnel (admin cancelled?) - still connected")
			return
		}
		u.svc = nil
	}
	u.connected = false
	u.connectBtn.SetText("Connect")
	u.connectBtn.Enable()
	u.status.SetText("disconnected")
	u.exitIP.SetText("Exit IP: -")
	u.log("disconnected")
}

func (u *ui) fail(msg string) {
	u.connectBtn.SetText("Connect")
	u.connectBtn.Enable()
	u.status.SetText("error")
	u.log("%s", msg)
}
