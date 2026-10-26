package pptxtest

import "encoding/base64"

// PNG is a 1×1 transparent PNG.
var PNG = mustBase64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

func mustBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// JourneyDeck is the deck the file-format journey uploads
// (e2e/fixtures/journey-deck.pptx). It holds 10 segments:
//
//   - slide 1: a title, and a two-sentence subtitle with a bold word (3)
//   - slide 2: a two-cell table, a picture's alt text, and notes (4)
//   - slide 3, hidden: a text box and a two-node SmartArt diagram (3)
func JourneyDeck() Deck {
	return Deck{Slides: []Slide{
		{
			Shapes: Placeholder(2, "title", P(R(`lang="en-US" dirty="0"`, "Quarterly results"))) +
				Placeholder(3, "subTitle", P(
					R(`lang="en-US" dirty="0"`, "Revenue grew in every region. "),
					R(`lang="en-US" b="1" dirty="0"`, "Costs"),
					R(`lang="en-US" dirty="0"`, " fell."),
				)),
		},
		{
			Shapes: Table(2, []string{P(R(`lang="en-US"`, "Region")), P(R(`lang="en-US"`, "Growth"))}) +
				Picture(3, "Bar chart of revenue by region"),
			Notes: P(R(`lang="en-US" dirty="0"`, "Mention the new office.")),
			Image: PNG,
		},
		{
			Hidden: true,
			Shapes: TextBox(2, P(R(`lang="en-US"`, "Backup slide"))) + SmartArtFrame(3),
			SmartArt: &SmartArt{
				Points: DocPoint("{00000000-0000-0000-0000-000000000001}") +
					NodePoint("{00000000-0000-0000-0000-000000000002}", P(R(`lang="en-US"`, "Plan"))) +
					NodePoint("{00000000-0000-0000-0000-000000000003}", P(R(`lang="en-US"`, "Launch"))) +
					PresPoint("{00000000-0000-0000-0000-000000000012}", "{00000000-0000-0000-0000-000000000002}") +
					PresPoint("{00000000-0000-0000-0000-000000000013}", "{00000000-0000-0000-0000-000000000003}"),
				Drawing: DrawingShape("{00000000-0000-0000-0000-000000000012}", P(R(`lang="en-US" sz="2400"`, "Plan"))) +
					DrawingShape("{00000000-0000-0000-0000-000000000013}", P(R(`lang="en-US" sz="2400"`, "Launch"))),
			},
		},
	}}
}
