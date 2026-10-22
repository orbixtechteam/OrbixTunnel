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
	w := a.NewWindow("eVPN")
	w.Resize(fyne.NewSize(720, 480))

	u := &ui{win: w, selected: -1}
	u.build()
	u.reload()

	w.SetContent(u.content())
	w.ShowAndRun()
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

	u.fullTun = widget.NewCheck("Full tunnel (TUN, needs admin)", nil)
	u.connectBtn = widget.NewButton("Connect", u.onConnect)
	u.connectBtn.Importance = widget.HighImportance
	u.status = widget.NewLabel("disconnected")
	u.exitIP = widget.NewLabel("Exit IP: -")

	u.logView = widget.NewMultiLineEntry()
	u.logView.Disable()
	u.logView.SetMinRowsVisible(6)
}

func (u *ui) content() fyne.CanvasObject {
	importBtn := widget.NewButton("Import...", u.onImport)
	delBtn := widget.NewButton("Delete", u.onDelete)

	left := container.NewBorder(
		widget.NewLabelWithStyle("Profiles", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewGridWithColumns(2, importBtn, delBtn),
		nil, nil,
		u.list,
	)

	right := container.NewVBox(
		u.fullTun,
		u.connectBtn,
		u.status,
		u.exitIP,
		widget.NewSeparator(),
		widget.NewLabel("Log"),
		container.NewVScroll(u.logView),
	)

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
	fyne.Do(func() { u.logView.SetText(u.logView.Text + line) })
}

func (u *ui) setStatus(s string) { fyne.Do(func() { u.status.SetText(s) }) }

func (u *ui) onImport() {
	dialog.ShowFileOpen(func(rc fyne.URIReadCloser, err error) {
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
		fyne.Do(func() {
			u.connectBtn.SetText("Disconnect")
			u.connectBtn.Enable()
		})
		u.setStatus("connected")

		if ip, err := ExitIP(p.Socks); err == nil {
			fyne.Do(func() { u.exitIP.SetText("Exit IP: " + ip) })
			u.log("exit IP: %s", ip)
		} else {
			u.log("exit IP check failed: %v", err)
		}
	}()
}

func (u *ui) disconnect() {
	if u.svc != nil {
		u.svc.Stop()
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
	fyne.Do(func() {
		u.connectBtn.SetText("Connect")
		u.connectBtn.Enable()
		u.status.SetText("error")
	})
	u.log("%s", msg)
}
