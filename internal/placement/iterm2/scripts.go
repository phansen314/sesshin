package iterm2

// The scripts are fixed text (design-spec.md, The iTerm2 backend): every value
// reaches one as an argument of its `on run` handler, except send's text,
// which travels in a file. Each begins by returning when iTerm2 isn't running,
// before any `tell` block that would start it, and names iTerm2 only by its
// bundle ID, as `tell application "iTerm2"` would start it too. Inside the
// `tell` block, `tab` is iTerm2's tab class, so the scripts write character id
// 9, and `before` and `kind` are words iTerm2 defines, so every variable of
// theirs is named x-something. None uses `delay`. A script prints only UUIDs
// and tty paths, one record per line with tab-separated fields, or one of
// the words notRunning and notFound.
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
// per line, for Locate.
const ttyScript = `if application id "com.googlecode.iterm2" is not running then return "not-running"
tell application id "com.googlecode.iterm2"
set xOut to ""
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
set xOut to xOut & (unique id of xSes) & (character id 9) & (tty of xSes) & (character id 10)
end repeat
end repeat
end repeat
return xOut
end tell`

// launchScript creates the new session and prints its unique id. Arguments:
// 1 the caller's unique id, 2 the type (tab, split, or os-window), 3 the
// command, 4 the name ("" for none), then name and value of each user
// variable. Creating is the only step that can fail the script; the name, the
// variables, and selecting the caller again are tried inside `try`, since a
// new tab or window takes the focus and a split does not.
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
if xType is "split" then
tell xSes to set xNew to (split vertically with default profile command xCmd)
else if xType is "tab" then
tell xWin to set xMade to (create tab with default profile command xCmd)
set xNew to current session of xMade
else
set xMade to (create window with default profile command xCmd)
set xNew to current session of xMade
end if
set xNewId to unique id of xNew
try
if xTitle is not "" then tell xNew to set name to xTitle
end try
repeat with xI from 5 to xCount by 2
try
tell xNew to set variable named ("user." & (item xI of argv)) to (item (xI + 1) of argv)
end try
end repeat
try
if xType is "os-window" then select xWin
select xTab
select xSes
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
// is read outside the `tell` block, as UTF-8.
const pasteScript = `on run argv
if application id "com.googlecode.iterm2" is not running then return "not-running"
set xTarget to item 1 of argv
set xText to (read POSIX file (item 2 of argv) as «class utf8»)
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

// focusScript selects a session's window, its tab, and the session, then
// activates iTerm2. Arguments: 1 the session's unique id.
const focusScript = `on run argv
if application id "com.googlecode.iterm2" is not running then return "not-running"
set xTarget to item 1 of argv
tell application id "com.googlecode.iterm2"
repeat with xWin in windows
repeat with xTab in tabs of xWin
repeat with xSes in sessions of xTab
if (unique id of xSes) is xTarget then
select xWin
select xTab
select xSes
activate
return "ok"
end if
end repeat
end repeat
end repeat
return "not-found"
end tell
end run`
