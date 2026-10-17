package gioui

import (
	"image"
	"os"
	"path/filepath"
	"strings"

	"gioui.org/gesture"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/unit"
	"github.com/vsariola/sointu/tracker"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

// SampleBrowser is the panel of the Buffers tab that browses the folders and
// audio files under a root folder: up and down move through the rows and
// play the files, right and left expand and collapse folders or go out of
// them, Enter or a double click loads a file into the selected buffer and
// Escape stops the preview.
type SampleBrowser struct {
	list       *DragList
	click      gesture.Click
	rootBtn    *Clickable
	rootMenu   *MenuState
	parentBtn  *Clickable
	refreshBtn *Clickable
	autoBtn    *Clickable
	playBtn    *Clickable
	folderBtn  *Clickable
	filter     *Editor
}

func NewSampleBrowser(m *tracker.Model) *SampleBrowser {
	return &SampleBrowser{
		list:       NewDragList(m.Browser().List(), layout.Vertical),
		rootBtn:    new(Clickable),
		rootMenu:   new(MenuState),
		parentBtn:  new(Clickable),
		refreshBtn: new(Clickable),
		autoBtn:    new(Clickable),
		playBtn:    new(Clickable),
		folderBtn:  new(Clickable),
		filter:     NewEditor(true, true, text.Start),
	}
}

func (sb *SampleBrowser) Tags(level int, yield TagYieldFunc) bool {
	return yield(level, sb.list) && yield(level+1, &sb.filter.widgetEditor) && sb.rootMenu.Tags(level+1, yield)
}

// startRoot returns the folder that the browser shows first: the last
// folder, if it is still there, or else the home folder.
func startRoot(b *tracker.BrowserModel) string {
	if dir := b.LastFolder(); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

func (sb *SampleBrowser) update(gtx C, b *tracker.BrowserModel) {
	if b.Root() == "" {
		b.SetRoot(startRoot(b))
	}
	for {
		ev, ok := gtx.Event(
			key.Filter{Focus: sb.list, Name: key.NameLeftArrow},
			key.Filter{Focus: sb.list, Name: key.NameRightArrow},
			key.Filter{Focus: sb.list, Name: key.NameReturn},
			key.Filter{Focus: sb.list, Name: key.NameEnter},
			key.Filter{Focus: sb.list, Name: key.NameEscape},
			key.Filter{Focus: sb.list, Name: key.NameDeleteBackward},
		)
		if !ok {
			break
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameLeftArrow:
			b.Left()
		case key.NameRightArrow:
			b.Right()
		case key.NameReturn, key.NameEnter:
			b.Enter()
		case key.NameEscape:
			b.Stop()
		case key.NameDeleteBackward:
			b.Up()
		}
		sb.list.EnsureVisible(sb.list.TrackerList.Selected())
	}
	// Enter goes from the filter to the rows, Escape also clears it
	switch sb.filter.Update(gtx, b.Filter()) {
	case EditorEventSubmit:
		sb.list.Focus()
	case EditorEventCancel:
		b.Filter().SetValue("")
		sb.list.Focus()
	}
	for sb.folderBtn.Clicked(gtx) {
		sb.chooseFolder(TrackerFromContext(gtx))
	}
	for {
		e, ok := sb.click.Update(gtx.Source)
		if !ok {
			break
		}
		// the press of the first click selected the row
		if e.Kind == gesture.KindClick && e.NumClicks == 2 {
			b.Enter()
		}
	}
}

// chooseFolder asks for a folder with the panel of the system and browses
// it.
func (sb *SampleBrowser) chooseFolder(tr *Tracker) {
	if tr.Exploring || !canChooseFolder {
		return
	}
	tr.Exploring = true
	chooseFolder(tr.Browser().Root(), func(path string) {
		tr.Broker().ToModel <- tracker.MsgToModel{Data: func() {
			tr.Exploring = false
			if path != "" {
				tr.Browser().SetRoot(path)
			}
		}}
	})
}

func (sb *SampleBrowser) Layout(gtx C) D {
	tr := TrackerFromContext(gtx)
	th := tr.Theme
	b := tr.Browser()
	sb.update(gtx, b)

	header := func(gtx C) D {
		var roots []MenuChild
		for _, r := range b.Roots() {
			roots = append(roots, ActionMenuChild(b.ShowRoot(r.Path), r.Name, shortPath(r.Path), icons.FileFolder))
		}
		title := filepath.Base(b.Root())
		if title == "" || title == "." {
			title = "Folder"
		}
		// one line, cut with an ellipsis, so that a narrow browser keeps a
		// low header
		titleStyle := th.Button.Text
		titleStyle.MaxLines = 1
		titleStyle.TextSize = 12
		rootBtn := MenuBtn(sb.rootMenu, sb.rootBtn, title).
			WithBtnStyle(&titleStyle).WithPopupStyle(&th.Popup.ContextMenu).
			WithTip(b.Root() + "\nChoose the folder to browse")
		parentBtn := ActionIconBtn(b.Parent(), th, sb.parentBtn, icons.NavigationArrowUpward, "Browse the folder above (Backspace)")
		refreshBtn := ActionIconBtn(b.Refresh(), th, sb.refreshBtn, icons.NavigationRefresh, "Read the folders again")
		autoBtn := ToggleIconBtn(tr.AutoPreview(), th, sb.autoBtn, icons.AVRepeat, icons.AVRepeatOne,
			"Play a file when it is selected", "Do not play a file when it is selected,\nonly with the play button")
		folderStyle := &th.IconButton.Enabled
		if !canChooseFolder {
			folderStyle = &th.IconButton.Disabled
		}
		folderBtn := IconBtn(th, folderStyle, sb.folderBtn, icons.FileFolderOpen, "Choose the folder to browse")
		playBtn := ActionIconBtn(b.PlaySelected(), th, sb.playBtn, icons.AVPlayArrow, "Play the selected file")
		titleW := func(gtx C) D { return rootBtn.Layout(gtx, roots...) }
		buttons := []layout.FlexChild{
			layout.Rigid(playBtn.Layout),
			layout.Rigid(autoBtn.Layout),
			layout.Rigid(folderBtn.Layout),
			layout.Rigid(parentBtn.Layout),
			layout.Rigid(refreshBtn.Layout),
		}
		if gtx.Constraints.Max.X >= gtx.Dp(300) {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				append([]layout.FlexChild{layout.Flexed(1, titleW)}, buttons...)...)
		}
		// narrow: the title above the buttons
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return layout.W.Layout(gtx, titleW)
			}),
			layout.Rigid(func(gtx C) D {
				return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx, buttons...)
			}),
		)
	}

	filter := func(gtx C) D {
		return layout.Inset{Left: 4, Right: 4, Bottom: 4}.Layout(gtx, func(gtx C) D {
			return layoutField(gtx, th, func(gtx C) D {
				return sb.filter.Layout(gtx, b.Filter(), th, &th.InstrumentEditor.InstrumentComment, "Filter")
			})
		})
	}

	entries := b.Entries()
	elem := func(gtx C, i int) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		if i >= len(entries) {
			return D{Size: image.Pt(gtx.Constraints.Max.X, gtx.Dp(20))}
		}
		e := entries[i]
		icon := icons.ImageAudiotrack
		switch {
		case e.Dir && e.Expanded:
			icon = icons.FileFolderOpen
		case e.Dir:
			icon = icons.FileFolder
		}
		return layout.Inset{Top: 2, Bottom: 2}.Layout(gtx, func(gtx C) D {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(layout.Spacer{Width: unit.Dp(4 + 12*e.Depth)}.Layout),
				layout.Rigid(func(gtx C) D {
					s := gtx.Dp(14)
					gtx.Constraints = layout.Exact(image.Pt(s, s))
					return th.Icon(icon).Layout(gtx, th.InstrumentEditor.Presets.Results.UserDir.Color)
				}),
				layout.Rigid(layout.Spacer{Width: 4}.Layout),
				layout.Flexed(1, Label(th, &th.InstrumentEditor.Presets.Results.Builtin, e.Name).Layout),
			)
		})
	}
	list := func(gtx C) D {
		size := gtx.Constraints.Max
		fdl := FilledDragList(th, sb.list)
		dims := fdl.Layout(gtx, elem, nil)
		fdl.LayoutScrollBar(gtx)
		// over the rows, letting their presses through, for double clicks
		pass := pointer.PassOp{}.Push(gtx.Ops)
		area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
		sb.click.Add(gtx.Ops)
		area.Pop()
		pass.Pop()
		return dims
	}
	status := func(gtx C) D {
		text := b.Status()
		switch {
		case b.RootErr() != nil:
			text = b.RootErr().Error()
		case text == "" && len(entries) == 0 && b.Filter().Value() != "":
			text = "Nothing here matches the filter"
		case text == "" && len(entries) == 0:
			text = "No folders or audio files here"
		case text == "":
			text = "Enter: load into the buffer, Esc: stop"
		}
		return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx C) D {
			style := th.InstrumentEditor.Properties.Label
			style.MaxLines = 2
			return Label(th, &style, text).Layout(gtx)
		})
	}
	return Surface{Height: 5, Focus: tr.PatchPanel.TreeFocused(gtx)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(header),
			layout.Rigid(filter),
			layout.Flexed(1, func(gtx C) D {
				defer op.Offset(image.Point{}).Push(gtx.Ops).Pop()
				return list(gtx)
			}),
			layout.Rigid(status),
		)
	})
}

// shortPath writes the home folder of a path as ~.
func shortPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if path == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return filepath.Join("~", rest)
		}
	}
	return path
}
