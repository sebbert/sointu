package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/vsariola/sointu/tracker"
)

type (
	// Tool is one of the tools: what the sointu-mcp command tells the
	// client about it, and what a Host does when it is called.
	Tool struct {
		Name        string
		Description string
		// Args is the type of its arguments, a struct, for their schema
		Args reflect.Type
		// Local tools need no Host: Run answers them
		Local    bool
		ReadOnly bool
		call     func(h *Host, args json.RawMessage) (string, error)
	}

	noArgs struct{}

	unitTypesArgs struct {
		Type string `json:"type,omitempty" jsonschema:"a unit type, for its parameters with their ranges, defaults and displayed values; left out, all types in a line each"`
	}
	instrumentArgs struct {
		Instrument string `json:"instrument" jsonschema:"the index of the instrument, from 0, or its name"`
		Verbose    bool   `json:"verbose,omitempty" jsonschema:"also the range of each parameter"`
	}
	moduleArgs struct {
		Module  string `json:"module" jsonschema:"the name of the module, or its ID"`
		Verbose bool   `json:"verbose,omitempty" jsonschema:"also the range of each parameter"`
	}
	presetsArgs struct {
		Search string `json:"search,omitempty" jsonschema:"keeps the presets with this in their name or directory, e.g. BA or reverb"`
	}
	editUnitsArgs struct {
		Edits []tracker.RemoteUnitEdit `json:"edits" jsonschema:"the units to change, each by its ID"`
	}
	addUnitsArgs struct {
		Instrument string                  `json:"instrument,omitempty" jsonschema:"adds at the end of the units of this instrument (index from 0, or name), unless after or before is given"`
		Module     string                  `json:"module,omitempty" jsonschema:"adds at the end of the units of this module instead"`
		After      int                     `json:"after,omitempty" jsonschema:"adds after the unit with this ID"`
		Before     int                     `json:"before,omitempty" jsonschema:"adds before the unit with this ID"`
		Units      []tracker.RemoteNewUnit `json:"units" jsonschema:"the units, in their order"`
	}
	deleteUnitsArgs struct {
		Units []int `json:"units" jsonschema:"the IDs of the units to delete"`
	}
	moveUnitsArgs struct {
		Units      []int  `json:"units" jsonschema:"the IDs of the units to move, in the order they are to have"`
		Instrument string `json:"instrument,omitempty" jsonschema:"moves to the end of the units of this instrument, unless after or before is given"`
		Module     string `json:"module,omitempty" jsonschema:"moves to the end of the units of this module instead"`
		After      int    `json:"after,omitempty" jsonschema:"moves to after the unit with this ID"`
		Before     int    `json:"before,omitempty" jsonschema:"moves to before the unit with this ID"`
	}
	addInstrumentArgs struct {
		Name   string `json:"name,omitempty"`
		Preset string `json:"preset,omitempty" jsonschema:"an instrument preset, as name or directory/name (see list_presets); left out, the default instrument of the tracker"`
		Voices int    `json:"voices,omitempty" jsonschema:"the number of voices, 1 if left out: the most notes it plays at once"`
		Before string `json:"before,omitempty" jsonschema:"the index or name of the instrument to put it before"`
		After  string `json:"after,omitempty" jsonschema:"the index or name of the instrument to put it after"`
	}
	moveInstrumentArgs struct {
		Instrument string `json:"instrument" jsonschema:"the index of the instrument, from 0, or its name"`
		Before     string `json:"before,omitempty" jsonschema:"the index or name of the instrument to put it before"`
		After      string `json:"after,omitempty" jsonschema:"the index or name of the instrument to put it after"`
	}
	editInstrumentArgs struct {
		Instrument string  `json:"instrument" jsonschema:"the index of the instrument, from 0, or its name"`
		Name       *string `json:"name,omitempty"`
		Comment    *string `json:"comment,omitempty"`
		Voices     *int    `json:"voices,omitempty" jsonschema:"the number of voices: how many notes it plays at once"`
		Mute       *bool   `json:"mute,omitempty"`
		Solo       *bool   `json:"solo,omitempty" jsonschema:"true mutes all other instruments, false unmutes all"`
		Preset     string  `json:"preset,omitempty" jsonschema:"replaces the units, the name and the comment of the instrument with those of an instrument preset; the voices stay"`
	}
	deleteInstrumentArgs struct {
		Instrument string `json:"instrument" jsonschema:"the index of the instrument, from 0, or its name"`
	}
	addModuleArgs struct {
		Preset string `json:"preset,omitempty" jsonschema:"a module preset (see list_presets); left out, an empty module"`
		Name   string `json:"name,omitempty"`
		Inputs int    `json:"inputs,omitempty" jsonschema:"for an empty module: the number of signals its units expect on the stack, 0 to 8"`
	}
	editModuleArgs struct {
		Module       string                      `json:"module" jsonschema:"the name of the module, or its ID"`
		Name         *string                     `json:"name,omitempty"`
		Comment      *string                     `json:"comment,omitempty"`
		Inputs       *int                        `json:"inputs,omitempty" jsonschema:"the number of signals its units expect on the stack, 0 to 8"`
		Params       []tracker.RemoteModuleParam `json:"params,omitempty" jsonschema:"the parameters of the module from the first: an empty object leaves one as it is, and those beyond what the module has are added (at most 8)"`
		DeleteParams []int                       `json:"delete_params,omitempty" jsonschema:"the numbers, from 1, of parameters to delete; the ones after them move down"`
	}
	deleteModuleArgs struct {
		Module string `json:"module" jsonschema:"the name of the module, or its ID"`
	}
	bpmArgs struct {
		BPM int `json:"bpm" jsonschema:"1 to 999"`
	}
	stepsArgs struct {
		Steps int `json:"steps,omitempty" jsonschema:"how many steps, 1 if left out"`
	}
	renderNoteArgs struct {
		Instrument string                   `json:"instrument" jsonschema:"the index of the instrument, from 0, or its name"`
		Notes      []int                    `json:"notes,omitempty" jsonschema:"the notes to play at once, as the tracker and MIDI input number them: 60 is C-3 in the tracker, 130.8 Hz for an oscillator with transpose 64; 69 is A-3, 220 Hz; 36 is C-1. [60] if left out; at most as many notes as the instrument has voices"`
		HoldMs     int                      `json:"hold_ms,omitempty" jsonschema:"how long the notes are held, 500 if left out"`
		TailMs     int                      `json:"tail_ms,omitempty" jsonschema:"how long the render goes on after the release, 1000 if left out"`
		Output     string                   `json:"output,omitempty" jsonschema:"dry (the default): only what the instrument itself puts out, the outputs of the other instruments silenced. master: the song's output with only this instrument playing, through the instruments it sends to (buses, reverb, mastering)"`
		Edits      []tracker.RemoteUnitEdit `json:"edits,omitempty" jsonschema:"unit changes, as for edit_units, made only to the copy that is rendered: the song stays as it is. For comparing variants, e.g. a unit disabled, before changing the song"`
		Wav        bool                     `json:"wav,omitempty" jsonschema:"also writes the render to a temporary 32-bit float stereo wav file at 44100 Hz and returns its path, for analysis with other tools"`
	}
	playNoteArgs struct {
		Instrument string `json:"instrument" jsonschema:"the index of the instrument, from 0, or its name"`
		Note       int    `json:"note,omitempty" jsonschema:"a note as the tracker and MIDI input number them: 60 is C-3 in the tracker (130.8 Hz with transpose 64); 60 if left out"`
		DurationMs int    `json:"duration_ms,omitempty" jsonschema:"how long the note is held, 500 if left out"`
	}
)

// decode reads the arguments of a tool, refusing fields that it does not
// have, so that a misspelled one is told instead of ignored.
func decode[A any](raw json.RawMessage) (a A, err error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return a, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("the arguments are not valid: %w", err)
	}
	return a, nil
}

// modelTool makes a tool that runs on the goroutine of the model.
func modelTool[A any](name, description string, readOnly bool, run func(r *tracker.Remote, a A) (string, error)) Tool {
	return Tool{Name: name, Description: description, Args: reflect.TypeFor[A](), ReadOnly: readOnly,
		call: func(h *Host, raw json.RawMessage) (string, error) {
			a, err := decode[A](raw)
			if err != nil {
				return "", err
			}
			return onModel(h, func(r *tracker.Remote) (string, error) {
				return r.Call(func() (string, error) { return run(r, a) })
			})
		}}
}

// localTool makes a tool that needs no model.
func localTool[A any](name, description string, run func(a A) (string, error)) Tool {
	return Tool{Name: name, Description: description, Args: reflect.TypeFor[A](), Local: true, ReadOnly: true,
		call: func(h *Host, raw json.RawMessage) (string, error) {
			a, err := decode[A](raw)
			if err != nil {
				return "", err
			}
			return run(a)
		}}
}

// Run answers a Local tool.
func (t Tool) Run(args json.RawMessage) (string, error) { return t.call(nil, args) }

// FindTool returns the tool with the given name.
func FindTool(name string) (Tool, bool) {
	for _, t := range Tools() {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

const changeNote = " One step of the tracker's undo history; the user hears and sees it at once. Returns the units as they are after it, with any problem (e.g. a unit that lacks signals on the stack)."

// Tools returns the tools.
func Tools() []Tool {
	return []Tool{
		localTool("guide", "How sointu patches work, for working on one: the stack of signals, units and their parameters, sends, instruments and buses, modules, what costs bytes, and how to work with these tools. Read it once before changing a patch.",
			func(noArgs) (string, error) { return Guide, nil }),
		localTool("unit_types", "The unit types: all in a line each (what it does, its effect on the stack, its parameters), or one with the ranges, defaults and displayed values of its parameters.",
			func(a unitTypesArgs) (string, error) { return tracker.RemoteUnitTypes(a.Type) }),
		modelTool("get_selection", "What the user has open and selected in the tracker: the tab, the instrument, the module (Modules tab), the buffer (Buffers tab), the units and the parameter under the cursor, whether the song plays, and the cursor in the score. What the user means by this, here or the selected one.", true,
			func(r *tracker.Remote, _ noArgs) (string, error) { return r.Selection(true), nil }),
		modelTool("get_song", "The song of the tracker in overview: tempo, score in summary, the instruments, modules and buffers in a line each, and what is wrong with the patch.", true,
			func(r *tracker.Remote, _ noArgs) (string, error) { return r.Song(), nil }),
		modelTool("get_instrument", "An instrument with its units in order: each with its ID, type, the signals on the stack before and after it, and its parameters as name=value(what the tracker displays). Sends show what they modulate.", true,
			func(r *tracker.Remote, a instrumentArgs) (string, error) {
				return r.Instrument(a.Instrument, a.Verbose)
			}),
		modelTool("get_module", "A module of the song: its parameters, and its units like get_instrument, with their bindings to the parameters.", true,
			func(r *tracker.Remote, a moduleArgs) (string, error) { return r.Module(a.Module, a.Verbose) }),
		modelTool("list_presets", "The instrument presets, by directory (BA bass, DR drums, LEAD, PAD, UTIL buses and mastering, ...), and the module presets.", true,
			func(r *tracker.Remote, a presetsArgs) (string, error) { return r.Presets(a.Search), nil }),
		modelTool("edit_units", "Changes units, any number at once: parameters by name, disabled, comment, the bands of an eq unit, and for units of a module, bindings. Refused as a whole if a value is out of range."+changeNote, false,
			func(r *tracker.Remote, a editUnitsArgs) (string, error) { return r.EditUnits(a.Edits) }),
		modelTool("add_units", "Adds units in a row: after or before a unit, or at the end of an instrument or a module. Mind the stack: a source pushes a signal, an effect changes the top one, mulp/addp merge two, out pops."+changeNote, false,
			func(r *tracker.Remote, a addUnitsArgs) (string, error) {
				return r.AddUnits(a.Instrument, a.Module, a.After, a.Before, a.Units)
			}),
		modelTool("delete_units", "Deletes units by their IDs."+changeNote, false,
			func(r *tracker.Remote, a deleteUnitsArgs) (string, error) { return r.DeleteUnits(a.Units) }),
		modelTool("move_units", "Moves units, also to another instrument or module. They keep their IDs, so sends still reach them."+changeNote, false,
			func(r *tracker.Remote, a moveUnitsArgs) (string, error) {
				return r.MoveUnits(a.Units, a.Instrument, a.Module, a.After, a.Before)
			}),
		modelTool("get_changes", "What the user changed in the song since your previous call: instruments, units and parameters (by unit ID, with old and new values), modules, buffers, the score, the tempo. Every other tool also starts its answer with these changes when there are any.", true,
			func(r *tracker.Remote, _ noArgs) (string, error) { return "", nil }),
		modelTool("add_instrument", "Adds an instrument: an instrument preset, or the default instrument. Instruments run in order and buses process only the instruments before them, so without before or after it goes before the first bus (an instrument that reads a channel with in, e.g. the master chain)."+changeNote, false,
			func(r *tracker.Remote, a addInstrumentArgs) (string, error) {
				return r.AddInstrument(a.Name, a.Preset, a.Voices, a.Before, a.After)
			}),
		modelTool("move_instrument", "Moves an instrument before or after another one: changes which buses process it, and its MIDI channel if that is auto."+changeNote, false,
			func(r *tracker.Remote, a moveInstrumentArgs) (string, error) {
				return r.MoveInstrument(a.Instrument, a.Before, a.After)
			}),
		modelTool("edit_instrument", "Changes an instrument: name, comment, voices, mute, solo, or loads an instrument preset into it."+changeNote, false,
			func(r *tracker.Remote, a editInstrumentArgs) (string, error) {
				return r.EditInstrument(a.Instrument, a.Name, a.Comment, a.Voices, a.Mute, a.Solo, a.Preset)
			}),
		modelTool("delete_instrument", "Deletes an instrument. Sends from other instruments to its units lose their target."+changeNote, false,
			func(r *tracker.Remote, a deleteInstrumentArgs) (string, error) {
				return r.DeleteInstrument(a.Instrument)
			}),
		modelTool("add_module", "Adds a module to the song: a module preset, or an empty module to fill with add_units. To use it, add a unit of the type module with the parameter module set to its name."+changeNote, false,
			func(r *tracker.Remote, a addModuleArgs) (string, error) {
				return r.AddModule(a.Name, a.Preset, a.Inputs)
			}),
		modelTool("edit_module", "Changes a module: name, comment, inputs, and its parameters (names, defaults, added, deleted). Parameters of its units are bound to them with bind of edit_units."+changeNote, false,
			func(r *tracker.Remote, a editModuleArgs) (string, error) {
				return r.EditModule(a.Module, a.Name, a.Comment, a.Inputs, a.Params, a.DeleteParams)
			}),
		modelTool("delete_module", "Deletes a module. The module units using it are left without a module."+changeNote, false,
			func(r *tracker.Remote, a deleteModuleArgs) (string, error) { return r.DeleteModule(a.Module) }),
		modelTool("set_bpm", "Sets the tempo of the song. In a plugin, the song follows the host's tempo again when that changes.", false,
			func(r *tracker.Remote, a bpmArgs) (string, error) { return r.SetBPM(a.BPM) }),
		modelTool("undo", "Undoes the last steps of the tracker's undo history: also changes that the user made by hand.", false,
			func(r *tracker.Remote, a stepsArgs) (string, error) { return r.Undo(a.Steps) }),
		modelTool("redo", "Redoes steps that were undone.", false,
			func(r *tracker.Remote, a stepsArgs) (string, error) { return r.Redo(a.Steps) }),
		{Name: "render_note", Description: "Renders notes of one instrument offline, on a copy of the song, and measures the result: peak, rms, crest factor, DC, stereo correlation, pitch over time, octave bands, envelope, attack and tail. Nothing is heard and the song is not changed. This is how to hear: measure before and after a change. With edits, the copy is changed first, to compare variants.",
			Args: reflect.TypeFor[renderNoteArgs](), ReadOnly: true, call: renderNote},
		modelTool("play_note", "Plays a note of an instrument in the running tracker, so that the user hears it.", true,
			func(r *tracker.Remote, a playNoteArgs) (string, error) {
				note, ms := a.Note, a.DurationMs
				if note == 0 {
					note = 60
				}
				if ms == 0 {
					ms = 500
				}
				return r.PlayNote(a.Instrument, note, time.Duration(ms)*time.Millisecond)
			}),
	}
}

// Instructions is what an MCP client is told when it connects.
var Instructions = strings.TrimSpace(`
sointu is a modular software synthesizer and tracker for 4k/64k intros. These tools read and change the patch (the instruments and their units) of a tracker or plugin that the user has running, live: the user hears and sees every change at once and can undo it.

- Start with list_instances (if several run, pass instance to every tool), then get_song and get_instrument. Call guide once before changing a patch.
- Units are named by their ID (#12), instruments by index from 0 or by name, parameters by name. Parameter values are whole numbers, mostly 0 to 128; listings show what the tracker displays for them, e.g. frequency=40(686 Hz).
- The selection: line of get_song and get_instrument (and get_selection) tells what the user has open and selected in the tracker: "this unit", "here", "the selected instrument" mean that. It changes as the user clicks, so read it again when they refer to it.
- The user edits the patch in the tracker far more than through you, also between your calls: never rely on what you read earlier. Read an instrument right before changing it. An answer that starts with NOTE: the song changed, lists what the user changed; get_changes asks for that alone.
- Instruments run in order and a bus (an instrument that reads a channel with in, e.g. the master chain) hears only the instruments before it. add_instrument puts new ones before the first bus; mind the order when moving instruments, and the WARNING lines of get_song.
- Each change tool is one undo step and returns the units after it. Watch the stack numbers and any PROBLEM or WARNING line in what comes back.
- You cannot hear: use render_note before and after a change to check what it did (level, pitch, spectrum, envelope). The user judges the sound; play_note lets them hear a note.
`)
