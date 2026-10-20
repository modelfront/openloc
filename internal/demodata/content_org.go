package demodata

// The organization-only part of the dataset (Options.Organization).

const (
	aiMTEngineName = "Nordwind — LLM translation"
	aiMTPromptName = "Nordwind marketing translator"
	aiMTPromptText = "You translate marketing copy for Nordwind Travel.\n" +
		"Keep prices, dates and placeholders such as {count} exactly as they are.\n" +
		"Write warm, direct copy and never translate \"Nordwind Miles\"."

	fileFAQ = "loyalty-faq-draft.docx"

	templateNordwind = "Nordwind marketing (EN→DE/FR)"
	templateHelix    = "Helix regulated content (EN→DE)"
	tmProfileName    = "Nordwind marketing memories"
	tmProfileDesc    = "The Nordwind memory, read and write, with an 80% fuzzy threshold."

	analysisBooking = "Summer campaign — full analysis"
	analysisIFU     = "HX-200 chapter 3 — quote"
)

var faqRows = []row{
	{"How do I join Nordwind Miles?", preTM("Wie werde ich Mitglied bei Nordwind Miles?", 86), empty},
	{"Joining is free and takes two minutes.", preMT("Die Mitgliedschaft ist kostenlos und dauert zwei Minuten."), empty},
	{"When are Miles added to my account?", preMT("Wann werden Miles meinem Konto gutgeschrieben?"), empty},
	{"Miles are added 48 hours after check-out.", empty, empty},
	{"Do my Miles expire?", preTM("Verfallen meine Miles?", 78), empty},
	{"Your Miles never expire while your account is active.", empty, empty},
}

// aiOutcome is what the AI review step decided on one IFU segment.
type aiOutcome struct {
	source  string
	outcome string // approved | ape_approved | ape_needs_review | rejected
	text    string // the APE rewrite; empty when the text stayed
	note    string
	comment string // the AI's segment comment; empty for none
}

// ifuAIReview is the AI review of the IFU file: one of each outcome, on the
// machine-translated segments the reviewer has not touched.
var ifuAIReview = []aiOutcome{
	{source: "Close the door until it clicks.", outcome: "approved",
		note: "Accurate and in the imperative voice of the source."},
	{source: "Prime the set to remove all air.", outcome: "ape_approved",
		text: "Entlüften Sie das Set vollständig.",
		note: "\"Befüllen … um alle Luft zu entfernen\" is not the term used in HX IFUs; \"entlüften\" is."},
	{source: "Open the door and insert the administration set as shown in figure 3.2.", outcome: "ape_needs_review",
		text:    "Öffnen Sie die Klappe und legen Sie das Infusionsset wie in Abbildung 3.2 dargestellt ein.",
		note:    "The HX-200 has a flap, not a door.",
		comment: "I changed \"Tür\" to \"Klappe\" to match figure 3.2. Please check it against the German device labels."},
	{source: "The maximum flow rate is 999 mL/h.", outcome: "rejected",
		note:    "A flow rate is safety-relevant, so a person must confirm it.",
		comment: "The number and unit match the source. Confirm that the German labels also say \"ml/h\"."},
}

// notificationSpec is one notification for a demo member.
type notificationSpec struct {
	event, subject, preview string
	job                     func(bt built) int64
	daysAgo                 int
	read                    bool
}

var guideNotifications = []notificationSpec{
	{"assignment", "New task: AI review of HX-200-IFU-chapter-3.docx (German)",
		"The AI pass is done. Four segments are waiting for your review.",
		func(bt built) int64 { return bt.ifuJobs["de"] }, 0, false},
	{"deadline", "Review of summer-campaign-emails.html (German) is due soon",
		"Nordwind — Summer 2026 booking flow: the Review step is due in a few days.",
		func(bt built) int64 { return bt.emailJobs["de"] }, 1, false},
	{"rejection", "Translation of booking-flow-strings.json (French) was sent back",
		"\"Please keep the urgency in the price banner.\"",
		func(bt built) int64 { return bt.bookingJobs["fr"] }, 3, false},
	{"completion", "Kaido — Patch 1.4 release notes is complete",
		"Both languages passed review.",
		func(bt built) int64 { return bt.patchJobs["de"] }, 9, true},
	{"assignment", "New task: Review of summer-campaign-emails.html (German)",
		"The translation step is done.",
		func(bt built) int64 { return bt.emailJobs["de"] }, 12, true},
}

var linguistNotifications = []notificationSpec{
	{"assignment", "New task: Translation of booking-flow-strings.json (German)",
		"Nordwind — Summer 2026 booking flow",
		func(bt built) int64 { return bt.bookingJobs["de"] }, 2, false},
}
