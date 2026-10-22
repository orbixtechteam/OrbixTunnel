package main

// eVPN desktop client (Fyne). Manages multiple tunnel profiles and connects
// through the eVPN carrier. "Full tunnel" routes the whole device via sing-box
// (needs admin/root); otherwise it just runs the local SOCKS5 proxy.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	killSwitch *widget.Check
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
		shutdownHelper()
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

	prefs := fyne.CurrentApp().Preferences()
	u.fullTun = widget.NewCheck("Full tunnel (whole device - will ask for admin)", nil)
	u.killSwitch = widget.NewCheck("Kill switch (keep traffic blocked if the tunnel drops)", nil)
	u.fullTun.OnChanged = func(on bool) {
		prefs.SetBool("fullTunnel", on)
		if on {
			u.killSwitch.Enable()
		} else {
			u.killSwitch.Disable()
		}
	}
	u.killSwitch.OnChanged = func(on bool) { prefs.SetBool("killSwitch", on) }
	u.killSwitch.SetChecked(prefs.Bool("killSwitch"))
	u.fullTun.SetChecked(prefs.Bool("fullTunnel"))
	if !u.fullTun.Checked {
		u.killSwitch.Disable()
	}
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
	editBtn := widget.NewButton("Edit...", u.onEdit)
	delBtn := widget.NewButton("Delete", u.onDelete)

	left := container.NewBorder(
		widget.NewLabelWithStyle("Profiles", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewGridWithColumns(2, newBtn, importBtn, editBtn, delBtn),
		nil, nil,
		u.list,
	)

	controls := container.NewVBox(
		u.fullTun,
		u.killSwitch,
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

func (u *ui) onNew() { u.profileForm("New profile", nil) }

func (u *ui) onEdit() {
	p := u.current()
	if p == nil {
		dialog.ShowInformation("No profile", "Select a profile first.", u.win)
		return
	}
	if u.connected {
		dialog.ShowInformation("Busy", "Disconnect before editing.", u.win)
		return
	}
	u.profileForm("Edit profile", p)
}

// profileForm shows the create/edit dialog. With base == nil it creates a new
// profile; otherwise it edits base (renaming moves the file).
func (u *ui) profileForm(title string, base *Profile) {
	def := &Profile{Port: "8443", Path: "/api/v2/telemetry/ingest", Socks: "127.0.0.1:1080"}
	if base != nil {
		def = base
	}
	name := widget.NewEntry()
	name.SetText(def.Name)
	name.SetPlaceHolder("phone")
	server := widget.NewEntry()
	server.SetText(def.Server)
	server.SetPlaceHolder("203.0.113.10")
	port := widget.NewEntry()
	port.SetText(def.Port)
	path := widget.NewEntry()
	path.SetText(def.Path)
	token := widget.NewEntry()
	token.SetText(def.Token)
	token.SetPlaceHolder("64-hex token from the server")
	socks := widget.NewEntry()
	socks.SetText(def.Socks)
	tls := widget.NewCheck("TLS (wss)", nil)
	tls.SetChecked(def.TLS)

	items := []*widget.FormItem{
		{Text: "Name", Widget: name},
		{Text: "Server", Widget: server},
		{Text: "Port", Widget: port},
		{Text: "Path", Widget: path},
		{Text: "Token", Widget: token},
		{Text: "Local SOCKS", Widget: socks},
		{Text: "", Widget: tls},
	}
	dialog.ShowForm(title, "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		p := &Profile{
			Name:   strings.TrimSpace(name.Text),
			Server: strings.TrimSpace(server.Text),
			Port:   strings.TrimSpace(port.Text),
			Path:   strings.TrimSpace(path.Text),
			Token:  strings.TrimSpace(token.Text),
			Socks:  strings.TrimSpace(socks.Text),
			TLS:    tls.Checked,
		}
		if p.Name == "" {
			p.Name = "profile"
		}
		if n, err := strconv.Atoi(p.Port); p.Port != "" && (err != nil || n < 1 || n > 65535) {
			dialog.ShowError(errors.New("port must be a number between 1 and 65535"), u.win)
			return
		}
		if p.Path != "" && !strings.HasPrefix(p.Path, "/") {
			p.Path = "/" + p.Path
		}
		// don't silently overwrite a different profile that has the same name
		if _, err := os.Stat(profilePath(p.Name)); err == nil && (base == nil || profilePath(p.Name) != base.File) {
			dialog.ShowError(fmt.Errorf("a profile named %q already exists", p.Name), u.win)
			return
		}
		np, err := saveProfile(p)
		if err != nil {
			dialog.ShowError(err, u.win)
			return
		}
		if base != nil && base.File != "" && base.File != np.File {
			deleteProfile(base) // renamed: drop the old file
		}
		u.reload()
		u.selectFile(np.File)
		if base == nil {
			u.log("created profile %q", np.Name)
		} else {
			u.log("updated profile %q", np.Name)
		}
	}, u.win)
}

// selectFile selects the list row whose profile is stored at file.
func (u *ui) selectFile(file string) {
	for i, p := range u.profiles {
		if p.File == file {
			u.list.Select(i)
			return
		}
	}
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
		u.selectFile(p.File)
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

		u.watch(svc, full)

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

// watch reports carrier health. If the server becomes unreachable in full
// tunnel mode the TUN stays up (so nothing leaks) and we keep retrying. With
// the kill switch off, a tunnel that stays down for 15s is torn down so the
// device gets its normal internet back; with it on, traffic stays blocked
// until the carrier recovers or you disconnect.
func (u *ui) watch(svc *Service, full bool) {
	var downSince time.Time
	svc.Watch(func(up bool) {
		if !u.connected || u.svc != svc {
			return
		}
		if up {
			if !downSince.IsZero() {
				u.log("tunnel restored")
				downSince = time.Time{}
			}
			u.setStatus("connected")
			return
		}
		if downSince.IsZero() {
			downSince = time.Now()
			u.log("server unreachable - reconnecting")
		}
		if full && u.killSwitch.Checked {
			u.setStatus("reconnecting... (kill switch: traffic blocked)")
			return
		}
		u.setStatus("reconnecting...")
		if full && time.Since(downSince) > 15*time.Second {
			u.log("tunnel down for 15s - disconnecting (kill switch off)")
			u.disconnect()
		}
	})
}
