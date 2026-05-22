package tts

import (
	"strings"

	"github.com/DarlingGoose/jpndict/analysis"
)

func JapaneseToKana(text string, mode KanaMode) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || mode == KanaNone {
		return text, nil
	}

	a, err := analysis.AnalyzeSentence(text)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	for _, token := range a.Tokens {
		value := token.Pronunciation
		if value == "" {
			value = token.Reading
		}
		if value == "" {
			value = token.Surface
		}

		switch mode {
		case KanaHiragana:
			value = katakanaToHiragana(value)
		case KanaKatakana:
		default:
			value = token.Surface
		}
		b.WriteString(value)
	}

	return b.String(), nil
}

func katakanaToHiragana(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'ァ' && r <= 'ヶ' {
			return r - 0x60
		}
		return r
	}, s)
}
