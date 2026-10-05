// Package pick is sesshin's pickers, built on fzf (picker-spec.md): commands for
// a person at a terminal. They run no operation of their own: they compose
// list for what they show with the operations they run on the selection,
// each as its own call. Only the sesshin binary links it.
package pick
