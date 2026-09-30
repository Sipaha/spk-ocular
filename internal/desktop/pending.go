package desktop

import "fmt"

// windowTitle is the window's title when nothing waits.
const windowTitle = "SPK Ocular"

// pendingNotice follows the number of agents' plans waiting for the user's
// confirmation (P14): the window's title counts them, and a desktop
// notification goes out only when more wait than before (a decision or an
// expiry is not news).
type pendingNotice struct {
	last int
}

// update takes the number waiting now: the window's title and whether to
// notify.
func (p *pendingNotice) update(n int, lang string) (title string, notify bool) {
	notify = n > p.last
	p.last = n
	if n <= 0 {
		return windowTitle, false
	}
	if lang == "ru" {
		return fmt.Sprintf("%s — ждёт подтверждения (%d)", windowTitle, n), notify
	}
	return fmt.Sprintf("%s — waiting for confirmation (%d)", windowTitle, n), notify
}

// notification is the desktop notification's summary and body.
func notification(n int, lang string) (summary, body string) {
	if lang == "ru" {
		return "SPK Ocular: агент ждёт подтверждения", fmt.Sprintf("Ждут подтверждения: %d. Решите в окне SPK Ocular.", n)
	}
	return "SPK Ocular: an agent waits for confirmation", fmt.Sprintf("Waiting: %d. Decide in the SPK Ocular window.", n)
}
