package tracker_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/vsariola/sointu"
	"github.com/vsariola/sointu/tracker"
	"github.com/vsariola/sointu/vm"
)

type NullContext struct{}

func (NullContext) BPM() (bpm float64, ok bool) {
	return 0, false
}

type modelFuzzState struct {
	model     *tracker.Model
	clipboard []byte
	file      []byte
}

type myWriteCloser struct {
	*bytes.Buffer
}

func (mwc *myWriteCloser) Close() error {
	// Noop
	return nil
}

func (s *modelFuzzState) Iterate(yield func(string, func(p string, t *testing.T)) bool, seed int) {
	// Ints
	s.IterateInt("InstrumentVoices", s.model.Instrument().Voices(), yield, seed)
	s.IterateInt("TrackVoices", s.model.Track().Voices(), yield, seed)
	s.IterateInt("SongLength", s.model.Song().Length(), yield, seed)
	s.IterateInt("BPM", s.model.Song().BPM(), yield, seed)
	s.IterateInt("RowsPerPattern", s.model.Song().RowsPerPattern(), yield, seed)
	s.IterateInt("RowsPerBeat", s.model.Song().RowsPerBeat(), yield, seed)
	s.IterateInt("Step", s.model.Note().Step(), yield, seed)
	s.IterateInt("Octave", s.model.Note().Octave(), yield, seed)
	s.IterateInt("InstrumentTab", s.model.Instrument().Tab(), yield, seed)
	s.IterateInt("ModuleInputs", s.model.Module().Inputs(), yield, seed)
	s.IterateInt("ModuleParamDefault", s.model.Module().ParamDefault(1), yield, seed)
	s.IterateInt("ModuleBindingAt0", s.model.Module().BindingAt(1, false), yield, seed)
	s.IterateInt("ModuleBindingAt128", s.model.Module().BindingAt(1, true), yield, seed)
	s.IterateInt("ParamBinding", s.model.Params().Binding(), yield, seed)
	// Lists
	s.IterateList("Instruments", s.model.Instrument().List(), yield, seed)
	s.IterateList("Units", s.model.Unit().List(), yield, seed)
	s.IterateList("Tracks", s.model.Track().List(), yield, seed)
	s.IterateList("OrderRows", s.model.Order().RowList(), yield, seed)
	s.IterateList("NoteRows", s.model.Note().RowList(), yield, seed)
	s.IterateList("UnitSearchResults", s.model.Unit().SearchResults(), yield, seed)
	s.IterateList("PresetDirs", s.model.Preset().DirList(), yield, seed)
	s.IterateList("PresetResults", s.model.Preset().SearchResultList(), yield, seed)
	s.IterateList("Modules", s.model.Module().List(), yield, seed)
	// Bools
	s.IterateBool("Panic", s.model.Play().Panicked(), yield, seed)
	s.IterateBool("Recording", s.model.Play().IsRecording(), yield, seed)
	s.IterateBool("Playing", s.model.Play().Started(), yield, seed)
	s.IterateBool("InstrEnlarged", s.model.Play().TrackerHidden(), yield, seed)
	s.IterateBool("Effect", s.model.Track().Effect(), yield, seed)
	s.IterateBool("Follow", s.model.Play().IsFollowing(), yield, seed)
	s.IterateBool("UniquePatterns", s.model.Note().UniquePatterns(), yield, seed)
	s.IterateBool("LinkInstrTrack", s.model.Track().LinkInstrument(), yield, seed)
	s.IterateBool("ModuleParamBound1", s.model.Module().ParamBound(1), yield, seed)
	s.IterateBool("Unfold", s.model.Unit().Unfold(), yield, seed)
	s.IterateBool("ModuleParamBound2", s.model.Module().ParamBound(2), yield, seed)
	// Strings
	s.IterateString("FilePath", s.model.Song().FilePath(), yield, seed)
	s.IterateString("InstrumentName", s.model.Instrument().Name(), yield, seed)
	s.IterateString("InstrumentComment", s.model.Instrument().Comment(), yield, seed)
	s.IterateString("UnitSearchText", s.model.Unit().SearchTerm(), yield, seed)
	s.IterateString("ModuleName", s.model.Module().Name(), yield, seed)
	s.IterateString("ModuleParamName", s.model.Module().ParamName(1), yield, seed)
	// Actions
	s.IterateAction("AddTrack", s.model.Track().Add(), yield, seed)
	s.IterateAction("DeleteTrack", s.model.Track().Delete(), yield, seed)
	s.IterateAction("AddInstrument", s.model.Instrument().Add(), yield, seed)
	s.IterateAction("DeleteInstrument", s.model.Instrument().Delete(), yield, seed)
	s.IterateAction("AddUnitAfter", s.model.Unit().Add(false), yield, seed)
	s.IterateAction("AddUnitBefore", s.model.Unit().Add(true), yield, seed)
	s.IterateAction("DeleteUnit", s.model.Unit().Delete(), yield, seed)
	s.IterateAction("ClearUnit", s.model.Unit().Clear(), yield, seed)
	s.IterateAction("Undo", s.model.History().Undo(), yield, seed)
	s.IterateAction("Redo", s.model.History().Redo(), yield, seed)
	s.IterateAction("RemoveUnusedPatterns", s.model.Order().RemoveUnusedPatterns(), yield, seed)
	s.IterateAction("AddSemitone", s.model.Note().AddSemitone(), yield, seed)
	s.IterateAction("SubtractSemitone", s.model.Note().SubtractSemitone(), yield, seed)
	s.IterateAction("AddOctave", s.model.Note().AddOctave(), yield, seed)
	s.IterateAction("SubtractOctave", s.model.Note().SubtractOctave(), yield, seed)
	s.IterateAction("EditNoteOff", s.model.Note().NoteOff(), yield, seed)
	s.IterateAction("PlaySongStart", s.model.Play().FromBeginning(), yield, seed)
	s.IterateAction("AddOrderRowAfter", s.model.Order().AddRow(false), yield, seed)
	s.IterateAction("AddOrderRowBefore", s.model.Order().AddRow(true), yield, seed)
	s.IterateAction("DeleteOrderRowForward", s.model.Order().DeleteRow(false), yield, seed)
	s.IterateAction("DeleteOrderRowBackward", s.model.Order().DeleteRow(true), yield, seed)
	s.IterateAction("SplitInstrument", s.model.Instrument().Split(), yield, seed)
	s.IterateAction("SplitTrack", s.model.Track().Split(), yield, seed)
	s.IterateAction("AddModule", s.model.Module().Add(), yield, seed)
	s.IterateAction("DeleteModule", s.model.Module().Delete(), yield, seed)
	s.IterateAction("AddModuleParam", s.model.Module().AddParam(), yield, seed)
	s.IterateAction("DeleteModuleParam", s.model.Module().DeleteParam(1), yield, seed)
	s.IterateAction("MakeModule", s.model.Unit().MakeModule(), yield, seed)
	s.IterateAction("InlineModule", s.model.Unit().InlineModule(), yield, seed)
	s.IterateAction("UniqueModule", s.model.Unit().UniqueModule(), yield, seed)
	s.IterateAction("OpenModule", s.model.Unit().OpenModule(), yield, seed)
	yield("SetUnitTypeModule", func(p string, t *testing.T) { s.model.Unit().SetType("module") })
	yield("SetUnitTypeGain", func(p string, t *testing.T) { s.model.Unit().SetType("gain") })
	yield("ToggleUnfold", func(p string, t *testing.T) { s.model.Unit().ToggleUnfold(seed % 12).Do() })
	yield("UnitDisabled", func(p string, t *testing.T) { s.model.Unit().Disabled().Toggle() })
	yield("UnitComment", func(p string, t *testing.T) { s.model.Unit().Comment().SetValue(fmt.Sprintf("%d", seed)) })
	yield("UnitRows", func(p string, t *testing.T) {
		// every row has a unit, and the cursor is on one of the units
		// being edited
		l, params := s.model.Unit().List(), s.model.Params()
		for i := range l.Count() {
			if item := s.model.Unit().Item(i); item.Inner != (item.Depth > 0) {
				t.Errorf("Path: %s row %d: %+v", p, i, item)
			}
			for x := range params.RowWidth(i) {
				q := params.Item(tracker.Point{X: x, Y: i})
				if r := q.Range(); q.Type() != tracker.NoParameter && r.Max >= r.Min && (q.Value() < r.Min || q.Value() > r.Max) {
					t.Errorf("Path: %s row %d parameter %d (%s) value %d out of range [%d,%d]", p, i, x, q.Name(), q.Value(), r.Min, r.Max)
				}
				q.Hint()
				q.Bound()
			}
		}
		if c := l.Count(); c > 0 && !s.model.Unit().Item(l.Selected()).Selectable {
			t.Errorf("Path: %s the cursor is on row %d, not one of the units being edited", p, l.Selected())
		}
	})
	yield("ParamBindNew", func(p string, t *testing.T) {
		b := s.model.Params().Binding()
		b.SetValue(b.Range().Max)
	})
	yield("ParamUnbind", func(p string, t *testing.T) { s.model.Params().Binding().SetValue(0) })
	yield("InnerParam.Set", func(p string, t *testing.T) {
		// like the mouse: any parameter on any row, also not under the cursor
		params := s.model.Params()
		if h := params.Height(); h > 0 {
			y := seed % h
			if w := params.RowWidth(y); w > 0 {
				q := params.Item(tracker.Point{X: (seed >> 4) % w, Y: y})
				switch seed % 3 {
				case 0:
					q.SetValue(q.Range().Min + (seed>>8)%(max(q.Range().Max-q.Range().Min, 0)+1))
				case 1:
					q.Add(seed%5-2, seed%2 == 0)
				default:
					q.Reset()
				}
			}
		}
	})
	// Tables
	s.IterateTable("Order", s.model.Order().Table(), yield, seed)
	s.IterateTable("Notes", s.model.Note().Table(), yield, seed)
	s.IterateTable("Params", s.model.Params().Table(), yield, seed)
	// File reading
	if s.file != nil {
		yield("ReadSong", func(p string, t *testing.T) {
			reader := bytes.NewReader(s.file)
			readCloser := io.NopCloser(reader)
			s.model.Song().Read(readCloser)
		})
		yield("LoadInstrument", func(p string, t *testing.T) {
			reader := bytes.NewReader(s.file)
			readCloser := io.NopCloser(reader)
			s.model.Instrument().Read(readCloser)
		})
	}
	// File saving
	yield("WriteSong", func(p string, t *testing.T) {
		writer := bytes.NewBuffer(nil)
		writeCloser := &myWriteCloser{writer}
		s.model.Song().Write(writeCloser)
		s.file = writer.Bytes()
	})
	yield("SaveInstrument", func(p string, t *testing.T) {
		writer := bytes.NewBuffer(nil)
		writeCloser := &myWriteCloser{writer}
		s.model.Instrument().Write(writeCloser)
		s.file = writer.Bytes()
	})
	// the eq unit and its bands
	eq := s.model.EQ()
	yield("SetUnitTypeEQ", func(p string, t *testing.T) { s.model.Unit().SetType("eq") })
	s.IterateAction("EQAddBand", eq.AddBand(), yield, seed)
	s.IterateAction("EQDeleteBand", eq.DeleteBand(), yield, seed)
	s.IterateInt("EQType", eq.Type(), yield, seed)
	s.IterateBool("EQOn", eq.On(), yield, seed)
	yield("EQSelect", func(p string, t *testing.T) { eq.SetSelected(seed%20 - 2) })
	yield("EQSet", func(p string, t *testing.T) {
		// like a drag: a gesture of several changes
		i := eq.Selected()
		b, _ := eq.Band(i)
		eq.BeginGesture()
		for k := range seed%3 + 1 {
			b.Frequency = float64(seed>>4%30000) + float64(k)
			b.Gain = float64(seed>>8%100) - 50
			b.Q = float64(seed>>12%500) / 10
			eq.Set(i, b)
		}
		eq.EndGesture()
	})
	yield("EQStep", func(p string, t *testing.T) {
		eq.Step(eq.Selected(), float64(seed%7-3), float64(seed>>3%7-3), float64(seed>>6%7-3), seed%2 == 0)
	})
	yield("EQCheck", func(p string, t *testing.T) {
		c, _, ok := eq.Compiled()
		if ok != eq.Active() || ok && len(c.Bands) != eq.NumBands() {
			t.Errorf("Path: %s the eq is compiled to %d bands of %d", p, len(c.Bands), eq.NumBands())
		}
		if i := eq.Selected(); i < -1 || i >= eq.NumBands() {
			t.Errorf("Path: %s band %d of %d is selected", p, i, eq.NumBands())
		}
		eq.Info()
		eq.Units()
	})
}

func (s *modelFuzzState) IterateInt(name string, i tracker.Int, yield func(string, func(p string, t *testing.T)) bool, seed int) {
	r := i.Range()
	yield(name+".Set", func(p string, t *testing.T) {
		i.SetValue(seed%(r.Max-r.Min+10) - 5 + r.Min)
	})
	yield(name+".Value", func(p string, t *testing.T) {
		if v := i.Value(); v < r.Min || v > r.Max {
			r := i.Range()
			t.Errorf("Path: %s %s value out of range [%d,%d]: %d", p, name, r.Min, r.Max, v)
		}
	})
}

func (s *modelFuzzState) IterateAction(name string, a tracker.Action, yield func(string, func(p string, t *testing.T)) bool, seed int) {
	yield(name+".Do", func(p string, t *testing.T) {
		a.Do()
	})
}

func (s *modelFuzzState) IterateBool(name string, b tracker.Bool, yield func(string, func(p string, t *testing.T)) bool, seed int) {
	yield(name+".Set", func(p string, t *testing.T) {
		b.SetValue(seed%2 == 0)
	})
	yield(name+".Toggle", func(p string, t *testing.T) {
		b.Toggle()
	})
}

func (s *modelFuzzState) IterateString(name string, str tracker.String, yield func(string, func(p string, t *testing.T)) bool, seed int) {
	yield(name+".Set", func(p string, t *testing.T) {
		str.SetValue(fmt.Sprintf("%d", seed))
	})
}

func (s *modelFuzzState) IterateList(name string, l tracker.List, yield func(string, func(p string, t *testing.T)) bool, seed int) {
	yield(name+".SetSelected", func(p string, t *testing.T) {
		l.SetSelected(seed%50 - 16)
	})
	yield(name+".Count", func(p string, t *testing.T) {
		if c := l.Count(); c > 0 {
			if l.Selected() < 0 || l.Selected() >= c {
				t.Errorf("Path: %s %s selected out of range: %d", p, name, l.Selected())
			}
		} else {
			if l.Selected() != 0 {
				t.Errorf("Path: %s %s selected out of range: %d", p, name, l.Selected())
			}
		}
	})
	yield(name+".SetSelected2", func(p string, t *testing.T) {
		l.SetSelected2(seed%50 - 16)
	})
	yield(name+".Count2", func(p string, t *testing.T) {
		if c := l.Count(); c > 0 {
			if l.Selected2() < 0 || l.Selected2() >= c {
				t.Errorf("Path: %s List selected2 out of range: %d", p, l.Selected2())
			}
		} else {
			if l.Selected2() != 0 {
				t.Errorf("Path: %s List selected2 out of range: %d", p, l.Selected2())
			}
		}
	})
	yield(name+".Next", func(p string, t *testing.T) { // like the arrow keys
		l.SetSelected(l.Selected() + 1)
		l.SetSelected2(l.Selected())
	})
	yield(name+".Prev", func(p string, t *testing.T) {
		l.SetSelected(l.Selected() - 1)
		l.SetSelected2(l.Selected())
	})
	yield(name+".SelectAll", func(p string, t *testing.T) { l.SelectAll() })
	yield(name+".ExtendSelection", func(p string, t *testing.T) {
		l.ExtendSelection(seed%5 - 2)
	})
	yield(name+".MoveElements", func(p string, t *testing.T) {
		l.MoveElements(seed%2*2 - 1)
	})
	yield(name+".DeleteElementsForward", func(p string, t *testing.T) {
		l.DeleteElements(false)
	})
	yield(name+".DeleteElementsBackward", func(p string, t *testing.T) {
		l.DeleteElements(true)
	})
	yield(name+".CopyElements", func(p string, t *testing.T) {
		s.clipboard, _ = l.CopyElements()
	})
	yield(name+".PasteElements", func(p string, t *testing.T) {
		l.PasteElements(s.clipboard)
	})
}

func (s *modelFuzzState) IterateTable(name string, table tracker.Table, yield func(string, func(p string, t *testing.T)) bool, seed int) {
	yield(name+".SetCursor", func(p string, t *testing.T) {
		table.SetCursor(tracker.Point{seed % 16, seed * 1337 % 16})
	})
	yield(name+".SetCursor2", func(p string, t *testing.T) {
		table.SetCursor2(tracker.Point{seed % 16, seed * 1337 % 16})
	})
	yield(name+".Cursor", func(p string, t *testing.T) {
		if c := table.Cursor(); c.X < 0 || (c.X >= table.Width() && table.Width() > 0) || c.Y < 0 || (c.Y >= table.Height() && table.Height() > 0) {
			t.Errorf("Path: %s Table cursor out of range: %v", p, c)
		}
	})
	yield(name+".Cursor2", func(p string, t *testing.T) {
		if c := table.Cursor2(); c.X < 0 || (c.X >= table.Width() && table.Width() > 0) || c.Y < 0 || (c.Y >= table.Height() && table.Height() > 0) {
			t.Errorf("Path: %s Table cursor2 out of range: %v", p, c)
		}
	})
	yield(name+".SetCursorX", func(p string, t *testing.T) {
		table.SetCursorX(seed % 16)
	})
	yield(name+".SetCursorY", func(p string, t *testing.T) {
		table.SetCursorY(seed % 16)
	})
	yield(name+".MoveCursor", func(p string, t *testing.T) {
		table.MoveCursor(seed%2*2-1, seed%2*2-1)
	})
	yield(name+".CursorRight", func(p string, t *testing.T) {
		table.MoveCursor(1, 0)
		table.SetCursor2(table.Cursor())
	})
	yield(name+".ExtendCursor", func(p string, t *testing.T) {
		table.ExtendCursor(seed%3-1, seed%5-2)
	})
	yield(name+".Copy", func(p string, t *testing.T) {
		s.clipboard, _ = table.Copy()
	})
	yield(name+".Paste", func(p string, t *testing.T) {
		table.Paste(s.clipboard)
	})
	yield(name+".Clear", func(p string, t *testing.T) {
		table.Clear()
	})
	yield(name+".Fill", func(p string, t *testing.T) {
		table.Fill(seed % 16)
	})
	yield(name+".Add", func(p string, t *testing.T) {
		table.Add((seed>>1)%16, seed%2 == 0)
	})
}

// fuzzStep is an operation of FuzzModel by its name, with a seed that ok
// accepts, if it is set: the operations take their arguments from the seed.
type fuzzStep struct {
	name string
	ok   func(seed int) bool
}

// fuzzSeed returns the input of FuzzModel that does the steps.
func fuzzSeed(f *testing.F, steps ...fuzzStep) []byte {
	broker := tracker.NewBroker()
	model := tracker.NewModel(broker, []sointu.Synther{vm.GoSynther{}}, tracker.NullMIDIContext{}, "")
	defer model.Close()
	state := modelFuzzState{model: model}
	var names []string
	state.Iterate(func(n string, _ func(p string, t *testing.T)) bool {
		names = append(names, n)
		return true
	}, 0)
	var ret []byte
	for _, step := range steps {
		index := slices.Index(names, step.name)
		if index < 0 {
			f.Fatalf("FuzzModel has no operation %v", step.name)
		}
		seed := index
		for step.ok != nil && !step.ok(seed) && seed >= 0 {
			if seed += len(names); seed > 1<<24 {
				seed = -1 // there is none for this number of operations: without the step
			}
		}
		if seed >= 0 {
			ret = binary.AppendVarint(ret, int64(seed))
		}
	}
	return ret
}

// innerUnitsSeed is an input of FuzzModel that makes a module of a unit of
// the first instrument, unfolds the module unit, and with the cursor on its
// inner units, changes their parameters, binds one, and adds, moves, copies,
// pastes and deletes units of the module, also in a module of the module.
func innerUnitsSeed(f *testing.F) []byte {
	var steps []fuzzStep
	for _, name := range []string{
		"AddUnitAfter.Do", "SetUnitTypeGain", "MakeModule.Do", "Unfold.Toggle", "UnitRows",
		"Units.Next", "UnitRows", "Params.CursorRight", "Params.Add", "Params.Clear", "Params.Fill", "InnerParam.Set",
		"ParamBindNew", "UnitRows", "Params.Add", "Params.Clear", "InnerParam.Set", "Undo.Do", "Redo.Do", "ParamUnbind", "ParamBindNew",
		"AddUnitAfter.Do", "SetUnitTypeGain", "UnitDisabled", "UnitComment", "Units.MoveElements",
		"Units.CopyElements", "Units.PasteElements", "UnitRows", "Units.ExtendSelection", "Params.ExtendCursor",
		"MakeModule.Do", "Unfold.Toggle", "Units.Next", "UnitRows", "InnerParam.Set", "Params.CursorRight", "ParamBindNew", "Params.Add",
		"AddUnitAfter.Do", "SetUnitTypeModule", "UnitRows", "Unfold.Toggle", "UnitRows", "InlineModule.Do", "UnitRows",
		"Units.Next", "DeleteUnit.Do", "DeleteUnit.Do", "DeleteUnit.Do", "DeleteUnit.Do", "Units.DeleteElementsForward", "UnitRows",
		"Undo.Do", "Undo.Do", "UnitRows", "Units.Prev", "Units.Prev", "Units.Prev", "UniqueModule.Do", "ToggleUnfold", "UnitRows",
		"Units.Next", "OpenModule.Do", "UnitRows", "Units.Next", "Unfold.Toggle", "Units.Next", "InnerParam.Set", "UnitRows",
	} {
		steps = append(steps, fuzzStep{name: name})
	}
	return fuzzSeed(f, steps...)
}

func FuzzModel(f *testing.F) {
	seed := make([]byte, 1)
	for i := range seed {
		seed[i] = byte(i)
	}
	f.Add(seed)
	f.Add(innerUnitsSeed(f))
	f.Fuzz(func(t *testing.T, slice []byte) {
		reader := bytes.NewReader(slice)
		synthers := []sointu.Synther{vm.GoSynther{}}
		broker := tracker.NewBroker()
		model := tracker.NewModel(broker, synthers, tracker.NullMIDIContext{}, "")
		defer model.Close()
		player := tracker.NewPlayer(broker, synthers[0])
		buf := make([][2]float32, 2048)
		closeChan := make(chan struct{})
		go func() {
		loop:
			for {
				select {
				case <-closeChan:
					break loop
				default:
					ctx := NullContext{}
					player.Process(buf, ctx)
				}
			}
		}()
		state := modelFuzzState{model: model}
		count := 0
		state.Iterate(func(n string, f func(p string, t *testing.T)) bool {
			count++
			return true
		}, 0)
		totalPath := ""
		for m, err := binary.ReadVarint(reader); err == nil; m, err = binary.ReadVarint(reader) {
			seed := int(m)
			index := seed % count
			state.Iterate(func(n string, f func(p string, t *testing.T)) bool {
				if index == 0 {
					totalPath += n + ". "
					f(totalPath, t)
				}
				index--
				return index > 0
			}, seed)
			for _, a := range model.Alerts().Iterate {
				if a.Name == "IDCollision" {
					t.Errorf("Path: %s Model has ID collisions", totalPath)
				}
				if a.Name == "InvalidUnitParameters" {
					t.Errorf("Path: %s Model units with invalid parameters", totalPath)
				}
			}
		}
		closeChan <- struct{}{}
		broker.CloseDetector <- struct{}{}
	})
}
