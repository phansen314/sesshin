package iterm2

// The scripts are fixed text (design-spec.md, The iTerm2 backend): every value
// reaches one as an argument of its `on run` handler, except send's text,
// which travels in a file. Each begins by returning when iTerm2 isn't running,
// before any `tell` block that would start it, and names iTerm2 only by its
// bundle ID, as `tell application "iTerm2"` would start it too. Inside the
// `tell` block, `tab` is iTerm2's tab class, so the scripts write character id
// 9, and `before` and `kind` are words iTerm2 defines, so every variable of
// theirs is named x-something. None uses `delay`. A script prints UUIDs and
// tty paths, one record per line with tab-separated fields, and never a name
// or a title; or one of the words not-running, not-found, ok, and (the launch
// script's) created-unknown. The Go constants for them are in run.go.
//
// Arguments are numbered from 1 in each script's comment.

// listScript prints the unique id of every session, one per line, for Exist.
const listScript = `if application id "com.googlecode.iterm2" is not running then return "not-running"
tell application id "com.googlecode.iterm2"
set xOut to ""
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
set xOut to xOut & (unique id of xSes) & (character id 10)
end repeat
end repeat
end repeat
return xOut
end tell`

// ttyScript prints the unique id and tty of every session, tab-separated, one
// per line, for Locate. A session whose tty can't be read, or is missing, has
// an empty field.
const ttyScript = `if application id "com.googlecode.iterm2" is not running then return "not-running"
tell application id "com.googlecode.iterm2"
set xOut to ""
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
set xTty to ""
try
set xValue to tty of xSes
if xValue is not missing value then set xTty to xValue as text
end try
set xOut to xOut & (unique id of xSes) & (character id 9) & xTty & (character id 10)
end repeat
end repeat
end repeat
return xOut
end tell`

// launchScript creates the new session and prints its unique id. Arguments:
// 1 the caller's unique id, 2 the type (tab, split, or os-window), 3 the
// command, 4 the name ("" for none), then name and value of each user
// variable. Creating is the only step that can fail the script. Reading the
// new session's id comes after it, inside `try`: when that fails the script
// prints created-unknown, since a session exists and nothing can name it. The
// name, the variables, and selecting again what was current are tried inside
// `try` too. A
// new tab takes its window's focus and a new window iTerm2's, and a split
// takes neither, so a tab puts back the session that was current in the
// caller's window, and an os-window the window that was. Both are found again
// by ID: xWin and xTab are positions, and a new window shifts them.
const launchScript = `on run argv
if application id "com.googlecode.iterm2" is not running then return "not-running"
set xCaller to item 1 of argv
set xType to item 2 of argv
set xCmd to item 3 of argv
set xTitle to item 4 of argv
set xCount to count of argv
tell application id "com.googlecode.iterm2"
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
if (unique id of xSes) is xCaller then
set xWinId to id of xWin
set xWasWin to xWinId
set xWasSes to xCaller
set xNewId to ""
try
set xWasWin to id of current window
set xWasSes to unique id of current session of xWin
end try
if xType is "split" then
tell xSes to set xNew to (split vertically with default profile command xCmd)
else if xType is "tab" then
tell xWin to set xMade to (create tab with default profile command xCmd)
else
set xMade to (create window with default profile command xCmd)
end if
try
if xType is not "split" then set xNew to current session of xMade
set xNewId to unique id of xNew
end try
if xNewId is "" then return "created-unknown"
try
if xTitle is not "" then tell xNew to set name to xTitle
end try
repeat with xI from 5 to xCount by 2
try
tell xNew to set variable named ("user." & (item xI of argv)) to (item (xI + 1) of argv)
end try
end repeat
try
if xType is "os-window" then
select (first window whose id is xWasWin)
else if xType is "tab" then
repeat with xOldTab in tabs of (first window whose id is xWinId)
repeat with xOldSes in sessions of xOldTab
if (unique id of xOldSes) is xWasSes then
select xOldTab
select xOldSes
end if
end repeat
end repeat
end if
end try
return xNewId
end if
end repeat
end repeat
end repeat
return "not-found"
end tell
end run`

// pasteScript writes the text of a file to a session, as typed, without a
// newline. Arguments: 1 the session's unique id, 2 the file's path. The file
// is read outside the `tell` block, as UTF-8, and before the check of whether
// iTerm2 runs, which is then the last thing before the `tell`: reading up to
// 1 MiB between the two would widen the gap in which iTerm2 could quit.
const pasteScript = `on run argv
set xTarget to item 1 of argv
set xText to (read POSIX file (item 2 of argv) as «class utf8»)
if application id "com.googlecode.iterm2" is not running then return "not-running"
tell application id "com.googlecode.iterm2"
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
if (unique id of xSes) is xTarget then
tell xSes to write text xText newline NO
return "ok"
end if
end repeat
end repeat
end repeat
return "not-found"
end tell
end run`

// enterScript writes a carriage return to a session. Arguments: 1 the
// session's unique id.
const enterScript = `on run argv
if application id "com.googlecode.iterm2" is not running then return "not-running"
set xTarget to item 1 of argv
tell application id "com.googlecode.iterm2"
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
if (unique id of xSes) is xTarget then
tell xSes to write text (character id 13) newline NO
return "ok"
end if
end repeat
end repeat
end repeat
return "not-found"
end tell
end run`

// focusScript selects a session, its tab, and its window, then activates
// iTerm2. The window comes last: selecting one moves it to the front of
// `windows`, and xTab and xSes are positions under xWin's old one. Arguments:
// 1 the session's unique id.
const focusScript = `on run argv
if application id "com.googlecode.iterm2" is not running then return "not-running"
set xTarget to item 1 of argv
tell application id "com.googlecode.iterm2"
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
if (unique id of xSes) is xTarget then
select xSes
select xTab
select xWin
activate
return "ok"
end if
end repeat
end repeat
end repeat
return "not-found"
end tell
end run`
