package chat

import (
	"errors"
	"net/http"

	"openloc/internal/ai"
)

// A model input carries at most one image: the screenshot of the newest
// message that has one. Every other screenshot is its text marker.

// imageRefusedNote replaces a screenshot the model cannot take.
const imageRefusedNote = "[The user sent a screenshot, but this model cannot view images. It goes with the " +
	"report to the support team; the visual part counts as unverified.]"

// oneImage keeps the image of the newest message that has one and drops the
// rest; their text markers stay.
func oneImage(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	kept := false
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Image == nil {
			continue
		}
		if kept {
			out[i].Image = nil
		}
		kept = true
	}
	return out
}

func hasImage(msgs []Message) bool {
	for _, m := range msgs {
		if m.Image != nil {
			return true
		}
	}
	return false
}

// withoutImages turns each image into the note that the model cannot view it.
func withoutImages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Image != nil {
			out[i].Image = nil
			out[i].Text += "\n\n" + imageRefusedNote
		}
	}
	return out
}

// imageRefused reports a provider's refusal of the request itself (a client
// error other than the key, the quota or the rate): what a vendor answers to
// an image its model cannot take.
func imageRefused(err error) bool {
	var perr *ai.ProviderError
	if !errors.As(err, &perr) || perr.Kind != ai.ProviderErrProvider {
		return false
	}
	switch perr.Status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return true
	}
	return false
}
