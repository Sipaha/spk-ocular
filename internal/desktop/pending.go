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
	if text, ok := pendingTranslations[lang]; ok {
		return fmt.Sprintf("%s — %s (%d)", windowTitle, text.waiting, n), notify
	}
	return fmt.Sprintf("%s — waiting for confirmation (%d)", windowTitle, n), notify
}

// notification is the desktop notification's summary and body.
func notification(n int, lang string) (summary, body string) {
	if lang == "ru" {
		return "SPK Ocular: агент ждёт подтверждения", fmt.Sprintf("Ждут подтверждения: %d. Решите в окне SPK Ocular.", n)
	}
	if text, ok := pendingTranslations[lang]; ok {
		return "SPK Ocular: " + text.summary, fmt.Sprintf(text.body, n)
	}
	return "SPK Ocular: an agent waits for confirmation", fmt.Sprintf("Waiting: %d. Decide in the SPK Ocular window.", n)
}

// Static native text also follows the persisted application language.
var pendingTranslations = map[string]struct{ waiting, summary, body string }{
	"zh": {"等待确认", "代理正在等待确认", "待确认：%d。请在 SPK Ocular 窗口中作出决定。"},
	"es": {"pendiente de confirmación", "un agente espera confirmación", "Pendientes: %d. Decida en la ventana de SPK Ocular."},
	"de": {"wartet auf Bestätigung", "ein Agent wartet auf Bestätigung", "Ausstehend: %d. Entscheiden Sie im Fenster von SPK Ocular."},
	"fr": {"en attente de confirmation", "un agent attend une confirmation", "En attente : %d. Décidez dans la fenêtre de SPK Ocular."},
	"pt": {"aguardando confirmação", "um agente aguarda confirmação", "Aguardando: %d. Decida na janela do SPK Ocular."},
	"ja": {"確認待ち", "エージェントが確認を待っています", "確認待ち：%d 件。SPK Ocular のウィンドウで判断してください。"},
}
