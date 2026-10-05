package model

// StateName is state.json's name in the state directory.
const StateName = "state.json"

// StateFile is state.json (design-spec.md, state.json).
type StateFile struct {
	Schema StateVersion `json:"schema"`
	LastID int64        `json:"last_id"`
}

// ReadState reads state.json's content. It is meaningful only when the result
// is usable.
func ReadState(data []byte) (StateFile, FileResult) {
	return readFile(data, StateSchema, func(s *StateFile, f *Fields, p *Problems) {
		if v, ok := f.Required("last_id"); ok {
			s.LastID, _ = p.Int(v, f.Ptr("last_id"), 0, MaxSafe)
		}
	})
}
