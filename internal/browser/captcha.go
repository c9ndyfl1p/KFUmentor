package browser

import (
	"strings"

	playwright "github.com/mxschmitt/playwright-go"
)

func captchaPresent(page playwright.Page) bool {
	if page == nil || page.IsClosed() {
		return false
	}
	currentURL := strings.ToLower(page.URL())
	if strings.Contains(currentURL, "captcha") || strings.Contains(currentURL, "smartcaptcha") {
		return true
	}
	for _, selector := range []string{
		"iframe[src*='captcha' i]",
		"[data-qa*='captcha' i]",
		"[class*='captcha' i]",
		"[id*='captcha' i]",
	} {
		if locator := page.Locator(selector); locator != nil {
			if count, err := locator.Count(); err == nil && count > 0 {
				for i := 0; i < count; i++ {
					if visible, err := locator.Nth(i).IsVisible(); err == nil && visible {
						return true
					}
				}
			}
		}
	}
	body, err := page.Locator("body").InnerText()
	if err != nil {
		return false
	}
	body = strings.ToLower(body)
	for _, phrase := range []string{
		"подтвердите, что вы не робот",
		"докажите, что вы не робот",
		"введите символы с картинки",
		"пройдите проверку",
		"проверка безопасности",
		"подтвердите, что вы человек",
	} {
		if strings.Contains(body, phrase) {
			return true
		}
	}
	return false
}
