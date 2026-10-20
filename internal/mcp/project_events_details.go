package mcp

import "strings"

// eventDetails returns a history event's details as the tool exposes them.
// The stored details serve the app's own history feed, which shows emails
// and raw error text to members; a connected client gets the product facts
// without them, at any depth:
//   - actor_name is dropped: it is the actor's email, snapshotted for
//     rendering. The event's top-level actor_name carries the name.
//   - any key ending in "email" (assignee_email, triggered_by_email, ...) is
//     dropped. Ids such as assignee_id stay; openloc_list_members resolves
//     them.
//   - error and error_* (error_dismissed) become true: the raw text can name
//     hosts and vendor responses, while the fact that a step failed or a
//     failure was dismissed is still worth knowing.
func eventDetails(details map[string]any) map[string]any {
	out := make(map[string]any, len(details))
	for k, v := range details {
		switch {
		case k == "actor_name" || strings.HasSuffix(k, "email"):
			continue
		case k == "error" || strings.HasPrefix(k, "error_"):
			out[k] = v != nil && v != ""
		default:
			out[k] = eventDetailValue(v)
		}
	}
	return out
}

func eventDetailValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return eventDetails(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = eventDetailValue(e)
		}
		return out
	default:
		return v
	}
}
