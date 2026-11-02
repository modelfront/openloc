package langcode

// DecimalSeparator returns the decimal separator CLDR gives the language,
// '.' or ',' — or 0 when the code is not a known language or its numbers use
// non-Latin digits (Arabic "١٫٥").
func DecimalSeparator(code string) rune {
	l, ok := LocaleFor(code)
	if !ok || l.NativeDigits || (l.Decimal != '.' && l.Decimal != ',') {
		return 0
	}
	return l.Decimal
}
