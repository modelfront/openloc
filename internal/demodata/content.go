package demodata

// The dataset: names, segments, memory entries and glossary terms.

// Names are the contract with e2e/docs-shots/screenshots.spec.ts.
const (
	customerNordwind = "Nordwind Travel"
	customerHelix    = "Helix Medical"
	customerKaido    = "Kaido Games"

	projectBooking = "Nordwind — Summer 2026 booking flow"
	projectIFU     = "Helix — HX-200 instructions for use, rev. C"
	projectPatch   = "Kaido — Patch 1.4 release notes"
	projectFAQ     = "Nordwind — Loyalty program FAQ"

	fileBooking = "booking-flow-strings.json"
	fileEmails  = "summer-campaign-emails.html"
	fileIFU     = "HX-200-IFU-chapter-3.docx"
	filePatch   = "patch-1-4-release-notes.md"

	tmNordwind = "Nordwind — Marketing (EN→DE/FR)"
	tmHelix    = "Helix — Instructions for use (EN→DE)"
	termBase   = "Nordwind brand glossary"
	qaProfile  = "Marketing QA"

	// One review engine per LLM provider, named after the work rather than the
	// model — the provider and model show in the row's chip, so a name like
	// "OpenAI review" would say the same thing twice.
	aiEngineName  = "Nordwind — brand review"
	aiEngineModel = "gpt-5"
	aiPromptName  = "Nordwind brand reviewer"
	aiPromptText  = "You review marketing translations for Nordwind Travel.\n" +
		"Reject anything that changes a price, a date or a cancellation window.\n" +
		"Keep the brand voice warm and direct, and never translate \"Nordwind Miles\"."

	aiClaudeSystem     = "Helix — IFU review"
	aiClaudeModel      = "claude-opus-5"
	aiClaudePrompt     = "Helix regulated-content reviewer"
	aiClaudePromptText = "You review German translations of medical device instructions for use.\n" +
		"Reject any change to a dose, a flow rate, a unit or a warning.\n" +
		"Keep the imperative voice the source uses, and never soften a WARNING."
)

// row is one segment with its translations. Status/origin drive what the
// editor shows: approved+edited = a human confirmed it (green badge); a draft
// is needs_review+edited; MT/TM rows with no editor are "Pretranslated"; an
// empty target is "Untranslated".
type row struct {
	src string
	de  tr
	fr  tr
}

type tr struct {
	text    string
	status  string // approved | needs_review | auto_approved | "" (untranslated)
	origin  string // tm | mt | ape | "" (human)
	percent int32  // TM match percent, 0 when not from TM
	human   bool   // edited_by set
	// mtDraft is the raw machine translation an APE rewrite replaced. Only an
	// `ape` origin has one, and it is what gives that segment's history two
	// rows instead of an APE row appearing from nowhere.
	mtDraft string
}

var approvedTM = func(t string, pct int32) tr {
	return tr{text: t, status: "approved", origin: "tm", percent: pct, human: true}
}
var human = func(t string) tr { return tr{text: t, status: "approved", human: true} }
var draft = func(t string) tr { return tr{text: t, status: "needs_review", human: true} }
var preMT = func(t string) tr { return tr{text: t, status: "needs_review", origin: "mt"} }
var preTM = func(t string, pct int32) tr { return tr{text: t, status: "needs_review", origin: "tm", percent: pct} }
var autoAPE = func(t, mt string) tr {
	return tr{text: t, status: "auto_approved", origin: "ape", mtDraft: mt}
}
var empty = tr{}

var bookingRows = []row{
	{"Find your next getaway.", approvedTM("Finden Sie Ihren nächsten Kurzurlaub.", 100), approvedTM("Trouvez votre prochaine escapade.", 100)},
	{"Where would you like to go?", approvedTM("Wohin möchten Sie reisen?", 101), preMT("Où souhaitez-vous aller ?")},
	{"Check-in", human("Anreise"), human("Arrivée")},
	{"Check-out", human("Abreise"), human("Départ")},
	{"2 adults, 1 room", draft("2 Erwachsene, 1 Zimmer"), preMT("2 adultes, 1 chambre")},
	{"Search", human("Suchen"), human("Rechercher")},
	// The German keeps the 48 from its 92% memory match while the source says
	// 24 — the fuzzy-match trap. It is the segment editor-sidebar.png features,
	// so the number check has something to report: do not "fix" it to 24.
	{"Free cancellation until 24 hours before check-in.", preTM("Kostenlose Stornierung bis 48 Stunden vor der Anreise.", 92), preMT("Annulation gratuite jusqu'à 24 heures avant l'arrivée.")},
	{"Breakfast included", approvedTM("Frühstück inklusive", 100), approvedTM("Petit-déjeuner inclus", 100)},
	{"Only {count} rooms left at this price!", draft("Nur noch {count} Zimmer zu diesem Preis!"), preMT("Plus que {count} chambres à ce prix !")},
	{"Total for {nights} nights", preMT("Gesamtpreis für {nights} Nächte"), preMT("Total pour {nights} nuits")},
	{"Taxes and fees included.", preTM("Inklusive Steuern und Gebühren.", 87), empty},
	{"Continue to payment", human("Weiter zur Zahlung"), preMT("Continuer vers le paiement")},
	{"Pay now", preMT("Jetzt bezahlen"), preMT("Payer maintenant")},
	{"Pay at the property", preMT("Vor Ort bezahlen"), empty},
	{"Your booking is confirmed.", approvedTM("Ihre Buchung ist bestätigt.", 100), approvedTM("Votre réservation est confirmée.", 100)},
	{"We sent the details to {email}.", autoAPE("Wir haben die Details an {email} gesendet.", "Wir schickten die Details zu {email}."), preMT("Nous avons envoyé les détails à {email}.")},
	{"Manage my booking", preMT("Meine Buchung verwalten"), empty},
	{"Add to calendar", preMT("Zum Kalender hinzufügen"), empty},
	{"Earn 250 Nordwind Miles with this stay.", draft("Sammeln Sie 250 Nordwind Miles mit diesem Aufenthalt."), empty},
	{"Something went wrong. Please try again.", approvedTM("Etwas ist schiefgelaufen. Bitte versuchen Sie es erneut.", 100), approvedTM("Une erreur s'est produite. Veuillez réessayer.", 100)},
	{"This room is no longer available.", preMT("Dieses Zimmer ist nicht mehr verfügbar."), empty},
	{"Show map", preMT("Karte anzeigen"), empty},
	{"Guest rating: {score} / 10", preMT("Gästebewertung: {score} / 10"), empty},
	{"Sort by price", empty, empty},
	{"Sort by rating", empty, empty},
	{"Filters", empty, empty},
}

var emailRows = []row{
	{"Summer is calling.", approvedTM("Der Sommer ruft.", 100), preMT("L'été vous appelle.")},
	{"Save up to 30% on stays booked before 30 June.", preTM("Sparen Sie bis zu 30 % auf Aufenthalte, die vor dem 30. Juni gebucht werden.", 95), empty},
	{"Book now", human("Jetzt buchen"), human("Réserver")},
	{"Our most-loved beach hotels", draft("Unsere beliebtesten Strandhotels"), empty},
	{"Handpicked by the Nordwind team.", preMT("Handverlesen vom Nordwind-Team."), empty},
	{"Members earn double Miles all summer.", preMT("Mitglieder sammeln den ganzen Sommer doppelte Miles."), empty},
	{"Not a member yet? Joining is free.", empty, empty},
	{"You are receiving this email because you booked with Nordwind Travel.", approvedTM("Sie erhalten diese E-Mail, weil Sie bei Nordwind Travel gebucht haben.", 100), empty},
	{"Unsubscribe", human("Abmelden"), human("Se désabonner")},
	{"View in browser", human("Im Browser anzeigen"), empty},
}

var ifuRows = []row{
	// "Vorbereitug" is a deliberate typo in the very first segment, so the
	// spell-check screenshot opens on a genuine misspelling instead of on a
	// product name the checker does not know. This file is pictured by no other
	// shot, so the typo cannot leak into the rest of the help pages.
	{"Chapter 3 — Preparing the pump for use", human("Kapitel 3 — Vorbereitug der Pumpe für den Gebrauch"), empty},
	{"Read this chapter completely before connecting the pump to a patient.", human("Lesen Sie dieses Kapitel vollständig, bevor Sie die Pumpe an einen Patienten anschließen."), empty},
	{"WARNING: Use only HX-series administration sets.", approvedTM("WARNUNG: Verwenden Sie ausschließlich Infusionssets der HX-Serie.", 100), empty},
	{"Inspect the housing for cracks or damage.", human("Prüfen Sie das Gehäuse auf Risse oder Beschädigungen."), empty},
	{"Do not use the device if the housing is damaged.", approvedTM("Verwenden Sie das Gerät nicht, wenn das Gehäuse beschädigt ist.", 100), empty},
	{"Press and hold the power button for 2 seconds.", human("Halten Sie die Ein/Aus-Taste 2 Sekunden lang gedrückt."), empty},
	{"The self-test takes about 15 seconds.", draft("Der Selbsttest dauert etwa 15 Sekunden."), empty},
	{"If the self-test fails, an error code is shown (see chapter 9).", draft("Schlägt der Selbsttest fehl, wird ein Fehlercode angezeigt (siehe Kapitel 9)."), empty},
	{"Open the door and insert the administration set as shown in figure 3.2.", preMT("Öffnen Sie die Tür und legen Sie das Infusionsset wie in Abbildung 3.2 gezeigt ein."), empty},
	{"Close the door until it clicks.", preMT("Schließen Sie die Tür, bis sie einrastet."), empty},
	{"Prime the set to remove all air.", preMT("Befüllen Sie das Set, um alle Luft zu entfernen."), empty},
	{"Set the flow rate in mL/h.", preTM("Stellen Sie die Förderrate in ml/h ein.", 88), empty},
	{"The maximum flow rate is 999 mL/h.", preMT("Die maximale Förderrate beträgt 999 ml/h."), empty},
	{"Confirm the settings before starting the infusion.", empty, empty},
	{"Start the infusion by pressing START.", empty, empty},
}

var patchRows = []row{
	{"Patch 1.4 — Release notes", human("Patch 1.4 — Versionshinweise"), human("Correctif 1.4 — Notes de version")},
	{"New: photo mode with adjustable depth of field.", human("Neu: Fotomodus mit einstellbarer Tiefenschärfe."), human("Nouveau : mode photo avec profondeur de champ réglable.")},
	{"New: two additional difficulty levels.", human("Neu: zwei zusätzliche Schwierigkeitsgrade."), human("Nouveau : deux niveaux de difficulté supplémentaires.")},
	{"Fixed: the map no longer flickers after fast travel.", human("Behoben: Die Karte flackert nach der Schnellreise nicht mehr."), human("Corrigé : la carte ne clignote plus après un voyage rapide.")},
	{"Fixed: save files larger than 2 GB load correctly.", human("Behoben: Speicherdateien über 2 GB werden korrekt geladen."), human("Corrigé : les sauvegardes de plus de 2 Go se chargent correctement.")},
	{"Improved: shorter loading times on all platforms.", human("Verbessert: kürzere Ladezeiten auf allen Plattformen."), human("Amélioré : temps de chargement réduits sur toutes les plateformes.")},
	{"Known issue: controller vibration may stop after resuming from sleep.", human("Bekanntes Problem: Die Controller-Vibration kann nach dem Aufwecken aus dem Ruhemodus aussetzen."), human("Problème connu : la vibration de la manette peut s'arrêter après une sortie de veille.")},
	{"Thank you for playing.", human("Danke, dass Sie spielen."), human("Merci de jouer.")},
}

type tmEntry struct{ src, de, fr string }

var nordwindTM = []tmEntry{
	{"Find your next getaway.", "Finden Sie Ihren nächsten Kurzurlaub.", "Trouvez votre prochaine escapade."},
	{"Where would you like to go?", "Wohin möchten Sie reisen?", "Où souhaitez-vous aller ?"},
	{"Breakfast included", "Frühstück inklusive", "Petit-déjeuner inclus"},
	{"Your booking is confirmed.", "Ihre Buchung ist bestätigt.", "Votre réservation est confirmée."},
	{"Something went wrong. Please try again.", "Etwas ist schiefgelaufen. Bitte versuchen Sie es erneut.", "Une erreur s'est produite. Veuillez réessayer."},
	{"Free cancellation until 48 hours before check-in.", "Kostenlose Stornierung bis 48 Stunden vor der Anreise.", "Annulation gratuite jusqu'à 48 heures avant l'arrivée."},
	{"Taxes and fees are included.", "Steuern und Gebühren sind inklusive.", "Taxes et frais inclus."},
	{"Summer is calling.", "Der Sommer ruft.", "L'été vous appelle."},
	{"Save up to 25% on stays booked before 31 May.", "Sparen Sie bis zu 25 % auf Aufenthalte, die vor dem 31. Mai gebucht werden.", "Économisez jusqu'à 25 % sur les séjours réservés avant le 31 mai."},
	{"You are receiving this email because you booked with Nordwind Travel.", "Sie erhalten diese E-Mail, weil Sie bei Nordwind Travel gebucht haben.", "Vous recevez cet e-mail car vous avez réservé avec Nordwind Travel."},
	{"Book now", "Jetzt buchen", "Réserver"},
	{"Unsubscribe", "Abmelden", "Se désabonner"},
	{"Members earn Miles on every stay.", "Mitglieder sammeln bei jedem Aufenthalt Miles.", "Les membres gagnent des Miles à chaque séjour."},
	{"Your Miles never expire.", "Ihre Miles verfallen nie.", "Vos Miles n'expirent jamais."},
	{"Welcome back, {name}!", "Willkommen zurück, {name}!", "Bon retour, {name} !"},
	{"See all destinations", "Alle Reiseziele anzeigen", "Voir toutes les destinations"},
	{"Best price guaranteed", "Bestpreisgarantie", "Meilleur prix garanti"},
	{"Pay in 3 instalments", "In 3 Raten zahlen", "Payer en 3 fois"},
}

var helixTM = []tmEntry{
	{"WARNING: Use only HX-series administration sets.", "WARNUNG: Verwenden Sie ausschließlich Infusionssets der HX-Serie.", ""},
	{"Do not use the device if the housing is damaged.", "Verwenden Sie das Gerät nicht, wenn das Gehäuse beschädigt ist.", ""},
	{"Set the flow rate in mL per hour.", "Stellen Sie die Förderrate in ml pro Stunde ein.", ""},
	{"Clean the housing with a soft, damp cloth.", "Reinigen Sie das Gehäuse mit einem weichen, feuchten Tuch.", ""},
	{"Contact Helix Medical service if the error persists.", "Wenden Sie sich an den Helix Medical Service, wenn der Fehler weiterhin besteht.", ""},
	{"Store between 5 °C and 40 °C.", "Bei 5 °C bis 40 °C lagern.", ""},
	{"Battery low", "Akku schwach", ""},
	{"Occlusion detected", "Okklusion erkannt", ""},
}

type term struct {
	en, de, fr string
	deStatus   string // approved | forbidden | pending
	definition string
}

var brandTerms = []term{
	{"Nordwind Miles", "Nordwind Miles", "Nordwind Miles", "approved", "Loyalty currency. Never translated, never lowercased."},
	{"getaway", "Kurzurlaub", "escapade", "approved", "A short leisure trip."},
	{"stay", "Aufenthalt", "séjour", "approved", ""},
	{"property", "Unterkunft", "hébergement", "approved", "The hotel, apartment or house. Not 'Eigentum'."},
	{"check-in", "Anreise", "arrivée", "approved", ""},
	{"check-out", "Abreise", "départ", "approved", ""},
	{"free cancellation", "kostenlose Stornierung", "annulation gratuite", "approved", ""},
	{"cheap", "billig", "pas cher", "forbidden", "Brand voice: say 'affordable' / 'günstig' instead."},
	{"affordable", "günstig", "abordable", "approved", ""},
	{"member", "Mitglied", "membre", "approved", ""},
	{"guest rating", "Gästebewertung", "note des voyageurs", "approved", ""},
	{"handpicked", "handverlesen", "sélectionné avec soin", "pending", ""},
}

// promptLanguageRule is one target-language addition to a prompt.
type promptLanguageRule struct {
	targets []string
	text    string
}

// nordwindLanguageRules are the Nordwind reviewer's rules per target
// language, appended to its instructions only for those languages.
var nordwindLanguageRules = []promptLanguageRule{
	{[]string{"de"}, "Address the reader informally with \"du\": that is Nordwind's German brand voice. Keep \"Nordwind Miles\" in English."},
	{[]string{"fr"}, "Address the reader with \"vous\". Use French typography: a non-breaking space before : ; ! and ?, and « » quotation marks."},
}
