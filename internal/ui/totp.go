package ui

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/totp"
)

// checkTOTPSecret validates a record's TOTP secret field before saving; an
// empty field is allowed (the record has no TOTP).
func checkTOTPSecret(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if _, err := totp.Parse(text); err != nil {
		return err
	}
	return nil
}

// spacedCode groups a code's digits for reading, as authenticator apps do:
// "123 456", "123 4567", "1234 5678".
func spacedCode(code string) string {
	half := len(code) / 2
	return code[:half] + " " + code[half:]
}

// totpPanel returns a panel showing the record's current one-time code, the
// seconds until it changes, and Copy Code, and a function that starts its
// once-a-second ticking; call it once the panel is shown. The ticking stops
// when the detail panel is re-rendered or cleared (stopTOTP), which also
// wipes the decoded key and blanks the code. A secret that cannot be read
// is reported instead.
func (dv *DetailView) totpPanel(secret string) (fyne.CanvasObject, func()) {
	key, err := totp.Parse(secret)
	if err != nil {
		problem := errorText()
		problem.SetText(errorMessage("Can't make codes", err) + ". Correct the TOTP secret and save.")
		problem.Show()
		return card(subheading("One-Time Code"), problem), func() {}
	}

	code := widget.NewLabel("")
	code.TextStyle = fyne.TextStyle{Monospace: true} // Fyne has no bold monospace font
	code.SizeName = theme.SizeNameHeadingText
	left := caption("")

	update := func() {
		now := dv.now()
		code.SetText(spacedCode(key.Code(now)))
		remaining := key.Remaining(now)
		secs := int((remaining + time.Second - 1) / time.Second)
		left.SetText(fmt.Sprintf("Changes in %d s", secs))
	}
	update()

	copyBtn := widget.NewButtonWithIcon("Copy Code", theme.ContentCopyIcon(), func() {
		dv.copyValue(key.Code(dv.now()))
	})
	panel := card(container.NewBorder(nil, nil, nil, container.NewCenter(copyBtn),
		container.NewHBox(container.NewCenter(subheading("One-Time Code")), code, container.NewCenter(left))))
	stop := func() {
		key.Wipe()
		code.SetText("")
	}
	return panel, func() { dv.startTOTP(update, stop) }
}

// startTOTP calls update at each whole second, on the main goroutine,
// until stopTOTP, which then calls stop, so a new code shows as soon as it
// is valid. Each start has its own generation, so a tick already queued for
// an older panel does nothing.
func (dv *DetailView) startTOTP(update, stop func()) {
	dv.stopTOTP()
	dv.totpStop = stop
	gen := dv.totpGen
	var schedule func()
	schedule = func() {
		next := time.Second - time.Duration(dv.now().Nanosecond())
		dv.totpTimer = dv.newTimer(next, func() {
			fyne.Do(func() {
				if gen != dv.totpGen {
					return
				}
				update()
				schedule()
			})
		})
	}
	schedule()
}

// stopTOTP stops the code panel's ticking, wipes its key and blanks its
// code (Fyne's renderer cache can keep the panel reachable for a while).
func (dv *DetailView) stopTOTP() {
	dv.totpGen++
	if dv.totpTimer != nil {
		dv.totpTimer.Stop()
		dv.totpTimer = nil
	}
	if dv.totpStop != nil {
		dv.totpStop()
		dv.totpStop = nil
	}
}
