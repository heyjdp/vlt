package ui

import (
	"crypto/sha256"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// defaultClipboardTTL is used when the configured TTL is not positive.
const defaultClipboardTTL = 30 * time.Second

// clipboardClearer copies secret values to the clipboard and clears them
// after a TTL, showing a countdown in the status bar. There is one per app,
// and all of its methods run on the Fyne main goroutine.
//
//   - Each copy restarts the countdown; an earlier copy's countdown can never
//     cut a later copy short (each copy has its own generation).
//   - On expiry, and on Clear, the clipboard is wiped only if it still holds
//     the value vlt put there. Something the user copied afterwards from
//     another app is left alone. The comparison uses a SHA-256 digest so no
//     second plaintext copy of the secret is kept.
//   - Clear (called on lock) wipes vlt's value immediately and stops the
//     countdown, leaving no timer running.
type clipboardClearer struct {
	clip     fyne.Clipboard
	status   *widget.Label
	ttl      func() time.Duration
	newTimer timerFactory

	gen       int
	timer     stoppableTimer
	digest    [sha256.Size]byte
	holding   bool // the clipboard was last written by Copy
	remaining int  // seconds left in the countdown
}

// newClipboardClearer returns a clearer writing to clip. ttl is read at each
// copy, so a change in Settings applies to the next copy.
func newClipboardClearer(clip fyne.Clipboard, status *widget.Label, ttl func() time.Duration, timers timerFactory) *clipboardClearer {
	return &clipboardClearer{clip: clip, status: status, ttl: ttl, newTimer: timers}
}

// Copy puts value on the clipboard and starts a fresh countdown. An empty
// value (for example a blank username) is not copied.
func (c *clipboardClearer) Copy(value string) {
	if value == "" {
		c.status.SetText("Nothing to copy")
		return
	}
	c.stop()
	c.clip.SetContent(value)
	c.digest = sha256.Sum256([]byte(value))
	c.holding = true

	ttl := c.ttl()
	if ttl <= 0 {
		ttl = defaultClipboardTTL
	}
	c.remaining = int((ttl + time.Second - 1) / time.Second)
	c.status.SetText(fmt.Sprintf("Copied to clipboard (clears in %ds)", c.remaining))
	c.schedule(c.gen)
}

// Clear stops the countdown and wipes the clipboard if it still holds the
// value vlt copied. It is called when the vault locks.
func (c *clipboardClearer) Clear() {
	c.stop()
	c.wipeIfOurs()
}

// stop cancels the countdown; ticks already queued see a newer generation
// and do nothing.
func (c *clipboardClearer) stop() {
	c.gen++
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}

// schedule arranges the next one-second tick for generation gen.
func (c *clipboardClearer) schedule(gen int) {
	c.timer = c.newTimer(time.Second, func() {
		fyne.Do(func() { c.tick(gen) })
	})
}

// tick advances the countdown for generation gen, clearing at zero.
func (c *clipboardClearer) tick(gen int) {
	if gen != c.gen {
		return // superseded by a newer copy, or cleared
	}
	c.remaining--
	if c.remaining > 0 {
		c.status.SetText(fmt.Sprintf("Clipboard clears in %ds", c.remaining))
		c.schedule(gen)
		return
	}
	c.timer = nil
	if c.wipeIfOurs() {
		c.status.SetText("Clipboard cleared")
	} else {
		c.status.SetText("")
	}
}

// wipeIfOurs clears the clipboard if it still holds the value Copy put
// there, and reports whether it did. An empty read is treated as ours:
// Fyne's desktop driver returns "" when the clipboard cannot be read (on
// Windows, while another program holds it open), and clearing an empty
// clipboard is harmless, whereas skipping would leave the secret behind.
func (c *clipboardClearer) wipeIfOurs() bool {
	if !c.holding {
		return false
	}
	c.holding = false
	content := c.clip.Content()
	if content != "" && sha256.Sum256([]byte(content)) != c.digest {
		return false // the user copied something else since; leave it
	}
	c.clip.SetContent("")
	return true
}
