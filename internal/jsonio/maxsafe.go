package jsonio

// MaxSafe is the largest integer every JSON reader holds exactly, 2^53 − 1:
// the maximum of the files' integer fields but received_ns, and of an
// integer a terminal backend stores.
const MaxSafe = 1<<53 - 1
