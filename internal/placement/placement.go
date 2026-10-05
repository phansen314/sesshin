package placement

// Multiplexed reports whether the environment is a tmux or screen session:
// their variables name some other window, so a session there is never placed
// (design-spec.md, Placement).
func Multiplexed(getenv func(string) string) bool {
	return getenv("TMUX") != "" || getenv("STY") != ""
}
