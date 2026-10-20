package metrics

// HistoryKindsForTest returns every history kind the registry places.
func HistoryKindsForTest() []string {
	return append(append([]string{}, originKinds...), statusKinds...)
}

// HistoryMeasuresForTest lists the measures read from translation_history.
func HistoryMeasuresForTest() []string {
	var out []string
	for name, m := range measures {
		if m.from == historyGraph {
			out = append(out, name)
		}
	}
	return out
}

// CompileForTest returns the SQL and arguments Run would execute.
func CompileForTest(tenantID int64, req Request) (string, []any, error) {
	c, err := compile(tenantID, req)
	if err != nil {
		return "", nil, err
	}
	return c.sql, c.args, nil
}
