package editor

import (
	terminal "github.com/gg582/gim/internal/screen"
)

func (e *Editor) syncTextFrame(dontSyncCursor bool) {
	textFrame := e.fillTextFrameFromTop()
	textFrame = e.fillEmptyLinesIfAnyWithTilde(textFrame)

	// Theme-aware draw (fallback to old API if styled API not available yet)
	if e.screen != nil && e.theme != nil {
		if hl, ok := e.theme.ResolveGroup("Normal"); ok {
			st := toTCellStyle(hl)
			// You must implement this new method in internal/screen/screen.go
			e.screen.DisplayTextFrameStyled(textFrame, st)
		} else {
			e.screen.DisplayTextFrame(textFrame)
		}
	} else {
		e.screen.DisplayTextFrame(textFrame)
	}

	if dontSyncCursor == false {
		e.syncCursor()
	}
}

func (e *Editor) fillEmptyLinesIfAnyWithTilde(textFrameTemp [][]rune) [][]rune {
	//Fill the empty lines in the frame with '~' char
	shouldTilde := true
	for i := len(textFrameTemp) - 1; i > 0; i-- {
		if textFrameTemp[i] != nil {
			shouldTilde = false
			continue
		}
		textFrameTemp[i] = make([]rune, e.screen.ScreenDim.X)
		if shouldTilde {
			textFrameTemp[i][0] = '~'
		}

	}
	return textFrameTemp
}

func (e *Editor) fillTextFrameFromTop() [][]rune {
	x := 0
	y := 0
	textFrameTemp := make([][]rune, e.screen.ScreenDim.Y-1)
	i := e.firstLineInFrame
	for {
		x = 0
		line := e.dataBuffer[i]
		for _, char := range line {
			if textFrameTemp[y] == nil {
				textFrameTemp[y] = make([]rune, e.screen.ScreenDim.X)
			}
			textFrameTemp[y][x] = char
			if x+1 == e.screen.ScreenDim.X {
				x = 0
				y++
			} else {
				x++
			}
		}

		if i+1 == e.getLinesCount() || y == e.screen.ScreenDim.Y-2 {
			break
		}
		i++
		y++
	}
	e.lastLineInFrame = i
	return textFrameTemp
}

func (e *Editor) syncCursor() {
	cursorPosScreen := terminal.Vertex{X: e.cursorPos.X, Y: e.cursorPos.Y}
	cursorPosScreen.Y -= e.firstLineInFrame
	for i := e.firstLineInFrame; i < e.cursorPos.Y; i++ {
		cursorPosScreen.Y += (len(e.dataBuffer[i]) / e.screen.ScreenDim.X)
	}
	for cursorPosScreen.X > e.screen.ScreenDim.X {
		cursorPosScreen.X -= e.screen.ScreenDim.X
		cursorPosScreen.Y++
	}

	e.syncTextFrame(true)
	e.screen.DisplayCursor(cursorPosScreen)
}

func (e *Editor) syncStatusBar() {
	// statusMessage has priority
	text := e.currentCommand
	if len(e.statusMessage) > 0 {
		text = e.statusMessage
	}

	// Theme-aware status bar
	if e.screen != nil && e.theme != nil {
		if hl, ok := e.theme.ResolveGroup("StatusLine"); ok {
			st := toTCellStyle(hl)
			// You must implement this new method in internal/screen/screen.go
			e.screen.DisplayStatusBarStyled(text, st)
			return
		}
	}

	// Fallback
	e.screen.DisplayStatusBar(text)
}
