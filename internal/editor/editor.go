package editor

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gg582/gim/internal/logger"

	"github.com/gdamore/tcell"
	"github.com/gg582/gim/internal/actions"
	"github.com/gg582/gim/internal/fs"
	terminal "github.com/gg582/gim/internal/screen"
	"github.com/gg582/gim/internal/theme"
)

type mode string

const (
	MODE_NORMAL       mode = "NORMAL"
	MODE_INSERT       mode = "INSERT"
	MODE_COMMAND_LINE mode = "COMMAND_LINE"
)

type cursorMovement string

const (
	CURSOR_UP    cursorMovement = "UP"
	CURSOR_DOWN  cursorMovement = "DOWN"
	CURSOR_RIGHT cursorMovement = "RIGHT"
	CURSOR_LEFT  cursorMovement = "LEFT"
)

type Editor struct {
	currentMode              mode
	statusMessage            string
	fileName                 string
	dataBuffer               []string
	startLine                int
	cursorPos                terminal.Vertex
	currentCommand           string
	lastUpDownCursorMovement cursorMovement
	firstLineInFrame         int
	lastLineInFrame          int
	screen                   *terminal.Screen
	isDirty                  bool
	userEventChannel         chan actions.Event

	theme *theme.Theme
}

// NewEditor is a contructor function for the Editor
func NewEditor(f string) (Editor, error) {
	e := Editor{fileName: f, currentMode: MODE_NORMAL, lastUpDownCursorMovement: CURSOR_UP, startLine: 0, firstLineInFrame: 0}

	var err error
	// TODO: Load only the required portion in memory instead of whole file
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.dataBuffer, err = fs.ReadFileToLines(ctx, f)
	e.statusMessage = fmt.Sprintf("\"%v\"", f)
	if err != nil {
		e.dataBuffer = make([]string, 1)
		e.statusMessage += " [New File]"
	} else {
		charCount := 0
		for _, line := range e.dataBuffer {
			charCount += len(line) + 1
		}
		e.statusMessage += fmt.Sprintf(" %vL, %vC", e.getLinesCount(), charCount)
	}

	e.cursorPos = terminal.Vertex{0, 0}

	s, err := terminal.NewScreen()
	if err != nil {
		logger.Debug.Panicf("Error starting the screen %v", err)
		return e, err
	}
	e.screen = s

	// Load theme from ~/.govimrc and apply Normal as default screen style (best-effort).
	e.loadThemeFromGovimrc()

	e.syncTextFrame(false)
	e.syncCursor()
	e.syncStatusBar()
	e.listenToEvents()
	return e, nil
}

func (e *Editor) listenToEvents() {
	e.userEventChannel = make(chan actions.Event)
	actions.EventStream(e.userEventChannel, e.screen.TerminalScreen())
	e.HandleUserActions()
}

// HandleUserActions - function that listens the user input channel and handles it appropriately
func (e *Editor) HandleUserActions() {
	for event := range e.userEventChannel {
		if event.Kind != "KEY_PRESS" {
			continue
		}
		switch e.currentMode {
		case MODE_INSERT:
			if event.Value == tcell.KeyEscape {
				e.currentMode = MODE_NORMAL
				e.statusMessage = ""
				e.syncStatusBar()
				e.fixHorizontalCursorOverflow()
				e.syncCursor()
				continue
			}
			if e.handleTextAreaCursorMovement(event) {
				e.syncCursor()
				continue
			}
			e.handleKeyInsertMode(event)
		case MODE_NORMAL:
			if e.handleTextAreaCursorMovement(event) {
				e.syncCursor()
				continue
			}
			e.handleKeyNormalMode(event)
		case MODE_COMMAND_LINE:
			if event.Value == tcell.KeyEscape {
				e.currentMode = MODE_NORMAL
				e.currentCommand = ""
				e.syncStatusBar()
				continue
			}
			if event.Value == tcell.KeyEnter {
				e.runCommand(e.currentCommand)
			}
			e.handleKeyCommandLineMode(event)
		}

	}
}

func (e *Editor) handleTextAreaCursorMovement(event actions.Event) bool {
	isProcessed := true
	rangeY := e.getLinesCount()
	switch event.Value {
	case tcell.KeyLeft:
		if e.cursorPos.X != 0 {
			e.cursorPos.X--
			e.syncCursor()
		}
	case tcell.KeyRight:
		e.cursorPos.X++
		e.fixHorizontalCursorOverflow()
		e.syncCursor()
	case tcell.KeyDown:
		if e.cursorPos.Y+1 != rangeY {
			e.cursorPos.Y++
			e.lastUpDownCursorMovement = CURSOR_DOWN
			e.fixHorizontalCursorOverflow()
			e.fixVerticalCursorOverflow()
			e.syncCursor()
		}
	case tcell.KeyUp:
		if e.cursorPos.Y != 0 {
			e.cursorPos.Y--
			e.lastUpDownCursorMovement = CURSOR_UP
			e.fixHorizontalCursorOverflow()
			if e.cursorPos.Y < e.firstLineInFrame {
				e.firstLineInFrame--
				if e.lastLineInFrame-e.firstLineInFrame > e.screen.ScreenDim.Y-1 {
					e.lastLineInFrame--
				}
			}
			e.syncCursor()
		}
	default:
		isProcessed = false
	}
	return isProcessed
}

func (e *Editor) fixVerticalCursorOverflow() {
	for e.cursorPos.Y > e.lastLineInFrame {
		if e.lastLineInFrame+1 >= e.getLinesCount() {
			return
		}
		e.lastLineInFrame++
		if e.lastLineInFrame-e.firstLineInFrame > e.screen.ScreenDim.Y-2 {
			e.firstLineInFrame++
		}
		for i := e.lastLineInFrame; i >= e.firstLineInFrame; i-- {
			e.firstLineInFrame += len(e.dataBuffer[i]) / e.screen.ScreenDim.X
		}
	}
}

func (e *Editor) fixHorizontalCursorOverflow() {
	rangeX := e.getCurrentLineLength()
	if e.currentMode != MODE_INSERT {
		rangeX--
		if rangeX < 0 {
			rangeX = 0
		}
	}
	if rangeX < e.cursorPos.X {
		e.cursorPos.X = rangeX
	}
}

func (e *Editor) quit(force bool) {
	if e.isDirty && !force {
		e.statusMessage = "E37: No write since last change (add ! to override)"
		return
	}
	e.screen.Close()
	os.Exit(0)
}

func (e *Editor) runCommand(cmdFull string) {
	cmd := cmdFull[1:]
	lineNum, err := strconv.Atoi(cmd)
	if err == nil {
		if lineNum > e.getLinesCount() {
			lineNum = e.getLinesCount()
		}
		if lineNum < 1 {
			lineNum = 1
		}
		e.cursorPos.Y = lineNum - 1
		e.fixVerticalCursorOverflow()
		e.syncTextFrame(false)
	} else {
		switch cmd {
		case "q":
			e.quit(false)
		case "q!":
			e.quit(true)
		case "wq", "x":
			if e.isDirty {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				fs.WriteLinesToFile(ctx, e.fileName, e.dataBuffer)
			}
			e.screen.Close()
			os.Exit(0)
		}
	}
	e.currentCommand = ""
	e.syncStatusBar()
	e.currentMode = MODE_NORMAL
}

func (e *Editor) getCurrentLineLength() int {
	return len(e.dataBuffer[e.cursorPos.Y])
}

func (e *Editor) getLinesCount() int {
	return len(e.dataBuffer)
}

func (e *Editor) switchToInsertMode() {
	e.currentMode = MODE_INSERT
	e.statusMessage = "-- INSERT --"
	e.syncStatusBar()
}

func (e *Editor) handleKeyNormalMode(event actions.Event) {
	switch event.Rune {
	case ':':
		e.currentMode = MODE_COMMAND_LINE
		e.statusMessage = ""
		e.handleKeyCommandLineMode(event)
	case 'i':
		e.switchToInsertMode()
	case 'A':
		e.cursorPos.X = e.getCurrentLineLength()
		e.switchToInsertMode()
		e.syncCursor()
	case 'G':
		l := e.getLinesCount() - 1
		if l < 0 {
			l = 0
		}
		e.cursorPos.Y = l
		e.fixVerticalCursorOverflow()
		e.syncTextFrame(false)
	}
}

func (e *Editor) handleKeyCommandLineMode(event actions.Event) {
	if event.Rune == 0 || event.Value == tcell.KeyEnter {
		return
	}
	if event.Value == tcell.KeyBackspace || event.Value == tcell.KeyBackspace2 {
		e.currentCommand = e.currentCommand[:len(e.currentCommand)-1]
	} else {
		e.currentCommand += string(event.Rune)
	}
	e.syncStatusBar()
}

func (e *Editor) handleKeyInsertMode(event actions.Event) {
	e.isDirty = true
	switch event.Value {
	case tcell.KeyEnter:
		newBuffer := make([]string, e.getLinesCount()+1)
		copy(newBuffer, e.dataBuffer)
		if e.cursorPos.Y < e.getLinesCount()-1 {
			for i := len(newBuffer) - 1; i > e.cursorPos.Y; i-- {
				newBuffer[i] = newBuffer[i-1]
			}
		}

		if e.cursorPos.X < e.getCurrentLineLength() {
			newBuffer[e.cursorPos.Y] = e.dataBuffer[e.cursorPos.Y][:e.cursorPos.X]
			newBuffer[e.cursorPos.Y+1] = e.dataBuffer[e.cursorPos.Y][e.cursorPos.X:]
		}
		e.cursorPos.Y++
		e.cursorPos.X = 0
		e.lastUpDownCursorMovement = CURSOR_DOWN
		e.dataBuffer = newBuffer
		e.fixVerticalCursorOverflow()
		e.syncTextFrame(false)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if e.cursorPos.X == 0 && e.cursorPos.Y == 0 {
			return
		}
		if e.cursorPos.X > 0 {
			// Delete a character in a line
			line := e.dataBuffer[e.cursorPos.Y]
			e.dataBuffer[e.cursorPos.Y] = line[:e.cursorPos.X-1] + line[e.cursorPos.X:]
			e.cursorPos.X--
		} else {
			// Merge the contents of this line to previous line
			newBuffer := make([]string, e.getLinesCount()-1)
			j := 0
			for i, line := range e.dataBuffer {
				if i == e.cursorPos.Y {
					continue
				}
				if i == e.cursorPos.Y-1 {
					line = line + e.dataBuffer[e.cursorPos.Y]
				}
				newBuffer[j] = line
				j++
			}
			e.cursorPos.X = len(e.dataBuffer[e.cursorPos.Y-1])
			e.cursorPos.Y--
			e.dataBuffer = newBuffer
		}
		e.syncTextFrame(false)
	default:
		if event.Rune == 0 {
			return
		}
		line := e.dataBuffer[e.cursorPos.Y]
		e.dataBuffer[e.cursorPos.Y] = line[:e.cursorPos.X] + string(event.Rune) + line[e.cursorPos.X:]
		e.cursorPos.X++
		e.syncTextFrame(false)
	}
}

/*
Theme loading / applying
*/

func (e *Editor) loadThemeFromGovimrc() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	rcPath := filepath.Join(home, ".govimrc")
	themeName, bg, ok := parseGovimrc(rcPath)
	if !ok || themeName == "" {
		return
	}

	themePath := filepath.Join(home, ".govim", "colors", themeName+".vim")
	f, err := os.Open(themePath)
	if err != nil {
		return
	}
	defer f.Close()

	th, err := theme.ParseVimColorscheme(f)
	if err != nil {
		return
	}

	if bg != "" {
		th.Background = bg
	}

	e.theme = th
	e.applyThemeToScreenDefault()
}

func parseGovimrc(path string) (themeName string, background string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		// Full-line vim comment
		if strings.HasPrefix(line, "\"") {
			continue
		}

		// Strip trailing comment starting with "
		if i := strings.Index(line, "\""); i >= 0 {
			line = strings.TrimSpace(line[:i])
			if line == "" {
				continue
			}
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "colorscheme", "colo":
			if len(fields) >= 2 {
				themeName = fields[1]
				ok = true
			}
		case "set":
			// set background=dark
			if len(fields) >= 2 && strings.HasPrefix(fields[1], "background=") {
				background = strings.TrimPrefix(fields[1], "background=")
			}
		}
	}

	// Ignore scanner error for config
	return themeName, background, ok
}

func (e *Editor) applyThemeToScreenDefault() {
	if e.theme == nil || e.screen == nil {
		return
	}

	hl, ok := e.theme.ResolveGroup("Normal")
	if !ok {
		return
	}

	st := toTCellStyle(hl)
	scr := e.screen.TerminalScreen()
	scr.SetStyle(st)
	scr.Clear()
	scr.Sync()
}

func toTCellColor(c theme.Color) tcell.Color {
	switch c.Kind {
	case theme.COLOR_RGB:
		return tcell.NewRGBColor(int32(c.Red), int32(c.Green), int32(c.Blue))
	case theme.COLOR_INDEX:
		return tcell.PaletteColor(c.Index)
	default:
		return tcell.ColorDefault
	}
}

func toTCellStyle(hl theme.TextHighlight) tcell.Style {
	st := tcell.StyleDefault.
		Foreground(toTCellColor(hl.Foreground)).
		Background(toTCellColor(hl.Background))

	if hl.TxtStyle.Bold {
		st = st.Bold(true)
	}
	if hl.TxtStyle.Underline {
		st = st.Underline(true)
	}
	if hl.TxtStyle.Reverse {
		st = st.Reverse(true)
	}
	// Italic is not reliably supported across terminals/tcell versions, so skip.
	return st
}

