package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/phansen314/sesshin/internal/buildinfo"
	"github.com/phansen314/sesshin/internal/jsonio"
	"github.com/phansen314/sesshin/internal/ops"
	"github.com/phansen314/sesshin/internal/pick"
)

// Exit codes (cli-spec.md, Exit codes).
const (
	ExitOK           = 0
	ExitError        = 1
	ExitUsage        = 2
	ExitNotDelivered = 3
)

// Env is what one invocation reads and writes.
type Env struct {
	Stdin     io.Reader // read only for --input -
	Stdout    io.Writer // one write: the envelope, or help text
	Stderr    io.Writer // at most one line, after stdout is delivered
	BuildInfo func() buildinfo.Info
	// Setup is what install and uninstall run against; nil is the running
	// process's (ops.OSSetup), which tests replace.
	Setup *ops.Setup
	// Spawn is what spawn runs against; nil is the running process's
	// (ops.OSSpawnEnv), which tests replace.
	Spawn *ops.SpawnEnv
	// Send is what send runs against; nil is the running process's
	// (ops.OSSendEnv), which tests replace.
	Send *ops.SendEnv
	// Focus is what focus runs against; nil is the running process's
	// (ops.OSFocusEnv), which tests replace.
	Focus *ops.FocusEnv
	// Pick is what restart runs against; nil is the running process's
	// (pick.OSEnv), which tests replace.
	Pick *pick.Env
	// Read is what list, show, prune, and migrate run against; nil is the running
	// process's (ops.OSReadEnv), which tests replace.
	Read *ops.ReadEnv
	// Getwd is the working directory spawn's --cwd is resolved against; nil
	// is the process's. Its environment is Spawn's.
	Getwd func() (string, error)
}

// read is the ReadEnv list, show, prune, and migrate run against.
func (e Env) read() ops.ReadEnv {
	if e.Read != nil {
		return *e.Read
	}
	return ops.OSReadEnv()
}

// spawn is the SpawnEnv spawn runs against.
func (e Env) spawn() ops.SpawnEnv {
	if e.Spawn != nil {
		return *e.Spawn
	}
	return ops.OSSpawnEnv()
}

// focus is the FocusEnv focus runs against.
func (e Env) focus() ops.FocusEnv {
	if e.Focus != nil {
		return *e.Focus
	}
	return ops.OSFocusEnv()
}

// send is the SendEnv send runs against.
func (e Env) send() ops.SendEnv {
	if e.Send != nil {
		return *e.Send
	}
	return ops.OSSendEnv()
}

// pick is the Env restart runs against.
func (e Env) pick() pick.Env {
	if e.Pick != nil {
		return *e.Pick
	}
	return pick.OSEnv()
}

func (e Env) getwd() (string, error) {
	if e.Getwd != nil {
		return e.Getwd()
	}
	return os.Getwd()
}

// setup is the Setup install and uninstall run against.
func (e Env) setup() ops.Setup {
	if e.Setup != nil {
		return *e.Setup
	}
	return ops.OSSetup(e.BuildInfo())
}

// Run runs the command line args, without the program name, and returns the
// exit code.
func Run(args []string, env Env) int {
	out, code, note := execute(commands, args, env)
	return deliver(env, out, code, note)
}

// execute runs args and returns what to write, the exit code, and the note
// for stderr ("" for none).
func execute(cmds []Command, args []string, env Env) ([]byte, int, string) {
	var help bytes.Buffer
	var result *ops.Envelope
	root := newRoot(cmds, env, &result)
	root.SetArgs(args)
	root.SetIn(env.Stdin)
	root.SetOut(&help)
	root.SetErr(io.Discard)
	err := root.Execute()
	switch {
	case result != nil:
		return envelopeLine(*result)
	case err != nil:
		return envelopeLine(ops.Failed(usage(err)))
	}
	return help.Bytes(), ExitOK, "" // --help
}

func newRoot(cmds []Command, env Env, result **ops.Envelope) *cobra.Command {
	root := &cobra.Command{
		Use:           "sesshin",
		Short:         "Record and show what your Claude Code sessions are doing",
		SilenceErrors: true,
		SilenceUsage:  true,
		// Bare sesshin is a usage error, and an unknown command is reported
		// here rather than by cobra so its problem names the token.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return usageErr(nil, "missing command")
			}
			if cmd.ArgsLenAtDash() == 0 && isCommand(cmd, args[0]) {
				return usageErr(&args[0], "the command must come before --")
			}
			return usageErr(&args[0], "unknown command")
		},
		RunE:              func(*cobra.Command, []string) error { return nil },
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return err })
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	for i := range cmds {
		root.AddCommand(newCommand(&cmds[i], env, result))
	}
	return root
}

func newCommand(c *Command, env Env, result **ops.Envelope) *cobra.Command {
	use := c.Name
	for _, a := range c.Arguments {
		use += " <" + a.Name + ">"
	}
	if c.Rest != "" {
		use += " [flags] [-- <" + c.RestName + "...>]"
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   c.Summary,
		Example: c.Example,
		Args: func(cmd *cobra.Command, args []string) error {
			positional, _ := split(c, cmd, args) // what follows -- is the command's own
			if len(positional) > len(c.Arguments) {
				return usageErr(&positional[len(c.Arguments)], "unexpected argument")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if e := checkShape(c, cmd, args); e != nil {
				return e
			}
			env := runCommand(c, cmd, args, env)
			*result = &env
			return nil
		},
	}
	fs := cmd.Flags()
	fs.SortFlags = false
	for _, o := range c.Options {
		switch o.Type {
		case Bool:
			fs.Bool(o.Name, false, o.Help)
		case List:
			fs.StringArray(o.Name, nil, o.Help) // not StringSlice, which would parse quotes
		case Map:
			fs.Var(&tokens{}, o.Name, o.Help)
		default: // Int, String: a string, so every value reaches the operation's checks
			fs.String(o.Name, "", o.Help)
		}
	}
	fs.StringP("input", "i", "", "read the whole operation input from `file` (- for stdin)")
	return cmd
}

// isCommand reports whether name is one of root's commands, as typed.
func isCommand(root *cobra.Command, name string) bool {
	for _, c := range root.Commands() {
		if !c.Hidden && c.Name() == name {
			return true
		}
	}
	return false
}

// split is the positional arguments, and the tokens after "--" that are the
// command's Rest.
func split(c *Command, cmd *cobra.Command, args []string) (positional, rest []string) {
	if dash := cmd.ArgsLenAtDash(); c.Rest != "" && dash >= 0 {
		return args[:dash], args[dash:]
	}
	return args, nil
}

// checkShape reports what cobra cannot: --input together with an argument or
// an option that sets an input field, and a missing required argument or
// option.
func checkShape(c *Command, cmd *cobra.Command, args []string) error {
	input := cmd.Flags().Changed("input")
	positional, _ := split(c, cmd, args)
	switch {
	case input && len(args) > 0:
		return usageErr(&args[0], "--input cannot be combined with arguments that set input fields")
	case !input && len(positional) < len(c.Arguments):
		return usageErr(nil, "missing required argument <"+c.Arguments[len(positional)].Name+">")
	}
	if !input && len(c.OneOf) > 0 {
		anyGiven := false
		for _, name := range c.OneOf {
			anyGiven = anyGiven || cmd.Flags().Changed(name)
		}
		if !anyGiven {
			return usageErr(nil, "missing required option --"+strings.Join(c.OneOf, " or --"))
		}
	}
	for i, o := range c.Options {
		given := cmd.Flags().Changed(o.Name)
		switch {
		case input && given:
			arg := "--" + o.Name
			return usageErr(&arg, "--input cannot be combined with options that set input fields")
		case !input && !given && o.Required:
			return usageErr(nil, "missing required option --"+o.Name)
		}
		// Two options that set the same field can't both be built.
		for _, earlier := range c.Options[:i] {
			if given && earlier.Field == o.Field && cmd.Flags().Changed(earlier.Name) {
				arg := "--" + o.Name
				return usageErr(&arg, "--"+earlier.Name+" and --"+o.Name+" set the same field")
			}
		}
	}
	return nil
}

// runCommand builds or reads the operation's input and runs it.
func runCommand(c *Command, cmd *cobra.Command, args []string, env Env) ops.Envelope {
	if !cmd.Flags().Changed("input") {
		obj, problems, e := buildInput(c, cmd, args, env)
		if e != nil {
			return ops.Failed(e)
		}
		return c.Run(Source{Object: obj, Problems: problems}, env)
	}
	path, _ := cmd.Flags().GetString("input")
	data, e := readInput(path, env)
	if e != nil {
		return ops.Failed(e)
	}
	return c.Run(Source{Data: data, FromFile: true}, env)
}

// usageError is a usage problem found by sesshin rather than cobra.
type usageError struct{ problem usageProblem }

// usageProblem is one item of usage-details' problems.
type usageProblem struct {
	Argument *string `json:"argument,omitempty"`
	Reason   string  `json:"reason"`
}

func (e *usageError) Error() string { return e.problem.Reason }

func usageErr(arg *string, reason string) error {
	return &usageError{usageProblem{Argument: arg, Reason: reason}}
}

// usage turns an error from cobra's Execute into a usage error, naming the
// offending token where the error says which it was.
func usage(err error) *ops.Error {
	var ue *usageError
	p := usageProblem{Reason: err.Error()}
	var notExist *pflag.NotExistError
	var noValue *pflag.ValueRequiredError
	var badValue *pflag.InvalidValueError
	switch {
	case errors.As(err, &ue):
		p = ue.problem
	case errors.As(err, &notExist):
		p.Argument = flagToken(notExist.GetSpecifiedName(), notExist.GetSpecifiedShortnames())
	case errors.As(err, &noValue):
		p.Argument = flagToken(noValue.GetSpecifiedName(), noValue.GetSpecifiedShortnames())
	case errors.As(err, &badValue):
		arg := "--" + badValue.GetFlag().Name
		p.Argument = &arg
	}
	msg := p.Reason
	if p.Argument != nil && !strings.Contains(msg, *p.Argument) { // pflag's messages often name it already
		msg += ": " + *p.Argument
	}
	return &ops.Error{Kind: ops.KindUsage, Message: msg, Details: map[string]any{"problems": []usageProblem{p}}}
}

// flagToken is the token pflag faults: a group of short options (without
// its "-"), else a long option's name.
func flagToken(name, shorts string) *string {
	t := "--" + name
	if shorts != "" {
		t = "-" + shorts
	}
	return &t
}

// envelopeLine encodes env as the one output line (cli-spec.md, Output),
// with its exit code and its note for stderr.
func envelopeLine(env ops.Envelope) ([]byte, int, string) {
	b, err := jsonio.MarshalLine(env)
	if err != nil {
		env = ops.Failed(&ops.Error{Kind: ops.KindInternal, Message: "encoding the envelope: " + err.Error()})
		if b, err = jsonio.MarshalLine(env); err != nil {
			panic(err) // a crash: outcome unknown
		}
	}
	switch {
	case env.OK:
		return b, ExitOK, warningsNote(len(env.Warnings))
	case env.Error.Kind == ops.KindUsage:
		return b, ExitUsage, errorNote(env.Error)
	}
	return b, ExitError, errorNote(env.Error)
}

func errorNote(e *ops.Error) string {
	return oneLine("sesshin: " + e.Kind + ": " + e.Message)
}

// warningsNote is the stderr line for a success with n warnings; they are
// counted, never listed.
func warningsNote(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "sesshin: 1 warning (see .warnings in the output)"
	}
	return fmt.Sprintf("sesshin: %d warnings (see .warnings in the output)", n)
}

// oneLine escapes control characters, so the stderr line stays one line.
func oneLine(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// deliver writes out in one write and closes stdout; exit codes 0-2 are
// reported only once both succeed. Only then is note, if any, written to
// stderr, as one line whose own failure is ignored: the result was delivered.
func deliver(env Env, out []byte, code int, note string) int {
	_, err := env.Stdout.Write(out)
	if c, ok := env.Stdout.(io.Closer); ok && err == nil {
		err = c.Close()
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "sesshin: result not delivered: %v\n", oneLine(err.Error()))
		return ExitNotDelivered
	}
	if note != "" {
		_, _ = io.WriteString(env.Stderr, note+"\n")
	}
	return code
}
