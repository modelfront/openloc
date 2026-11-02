package placeable

// Kind is what a placeable is.
type Kind string

const (
	KindNumber      Kind = "number"
	KindDate        Kind = "date"
	KindTime        Kind = "time"
	KindMeasurement Kind = "measurement"
	KindCurrency    Kind = "currency"
	KindURL         Kind = "url"
	KindEmail       Kind = "email"
	KindVariable    Kind = "variable"
	KindCode        Kind = "code"
)

// Placeable is one placeable of a text, in the order the text has them.
type Placeable struct {
	Kind  Kind   `json:"kind"`
	Text  string `json:"text"`
	Start int    `json:"-"`
	End   int    `json:"-"`
}
