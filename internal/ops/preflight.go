package ops

// CheckSetup is the check spawn, resume, and the pickers make before they
// look at anything else: the environment resolves to locations (environment),
// and config.toml reads (corrupt, or io).
func CheckSetup(env ReadEnv) *Error {
	_, _, e := loadSetup(env)
	return e
}

// CheckTerminal is terminal unavailable as spawn and resume raise it: nil
// when the caller runs where a window can be opened. The pickers make it
// before fzf starts, so a selection is never made only to fail.
func CheckTerminal(env SpawnEnv) *Error {
	_, e := callerWindow(env.Getenv)
	return e
}
