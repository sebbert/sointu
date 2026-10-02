package tracker

import (
	"encoding/json"
	"os"
	"slices"
	"time"

	"github.com/vsariola/sointu"
)

// Model implements the mutable state for the tracker program GUI.
//
// Go does not have immutable slices, so there's no efficient way to guarantee
// accidental mutations in the song. But at least the value members are
// protected.
// It is owned by the GUI thread (goroutine), while the player is owned by
// by the audioprocessing thread. They communicate using the two channels
type (
	// modelData is the part of the model that gets save to recovery file
	modelData struct {
		Song                    sointu.Song
		Cursor, Cursor2         Cursor
		LowNibble               bool
		InstrIndex, InstrIndex2 int
		UnitIndex, UnitIndex2   int
		ParamIndex              int
		UnitSearchIndex         int
		UnitSearchString        string
		UnitSearching           bool
		Octave                  int
		Step                    int
		FilePath                string
		ChangedSinceSave        bool
		RecoveryFilePath        string
		ChangedSinceRecovery    bool
		SendSource              int
		InstrumentTab           InstrumentTab
		BufferIndex             int
		PresetSearchString      string
		MIDIBindings            MIDIBindings
		// ModuleIndex is the selected module, whose units the unit editor
		// shows on the Modules tab
		ModuleIndex int
		// UnitPath tells which units are being edited when the cursor of the
		// unit editor is on an inner unit of an unfolded module unit: the IDs
		// of the module units that the cursor is inside, from the outermost.
		// UnitIndex and UnitIndex2 are then indices of the units of the
		// module of the last one. See rows.go.
		UnitPath []int `json:",omitempty"`
	}

	Model struct {
		d       modelData
		derived derivedModelData

		trackerHidden bool

		// delayFree tells, by the ID of every delay unit, if its delay
		// times are edited freely instead of on a grid; see
		// delayFreeParameter
		delayFree map[int]bool

		// spectra are the latest spectra of the sources in spectrumWatch,
		// which the player reports while the GUI asks for them, last at
		// spectrumAsked
		spectra       map[SpectrumSource]SpectrumMsg
		spectrumWatch []SpectrumSource
		spectrumAsked map[SpectrumSource]time.Time

		// expansion tells how the module units were expanded for the song
		// that the player last got
		expansion *sointu.Expansion
		// expanded is the patch of that song: the units that are played
		// for the inner units of the unfolded module units
		expanded sointu.Patch
		// rowCache holds the rows of the unit editor
		rowCache rowCache

		// eq is what the editor of the eq unit keeps: see EQModel
		eq eqState

		// modulePresets are the module presets: first those of the user,
		// userModulePresets of them, read from modulePresetPath or, if it is
		// empty, from the user's configuration directory; then those that
		// the tracker comes with
		modulePresets     []modulePreset
		userModulePresets int
		modulePresetPath  string
		// modulePresetAsked is the name of the module preset that the dialog
		// asks about: the one to delete, or to save over
		modulePresetAsked string

		// onChange, when set, is called after each change to the model data,
		// e.g. to tell a plugin host that its project has unsaved changes
		onChange func()

		prevUndoKind    string
		undoSkipCounter int
		undoStack       []modelData
		redoStack       []modelData

		changeLevel    int
		changeCancel   bool
		changeSeverity ChangeSeverity
		changeType     ChangeType

		panic          bool
		recording      bool
		playing        bool
		loop           Loop
		follow         bool
		quitted        bool
		uniquePatterns bool
		// when linkInstrTrack is false, editing an instrument does not change
		// the track. when true, editing an instrument changes the tracks (e.g.
		// reordering or deleting instrument can delete track)
		linkInstrTrack bool

		playerStatus PlayerStatus

		scopeData      scopeData
		detectorResult DetectorResult

		spectrum *Spectrum

		weightingType WeightingType
		oversampling  bool

		specAnSettings specAnSettings
		specAnEnabled  bool

		alerts []Alert
		dialog Dialog

		syntherIndex   int              // the index of the synther used to create new synths
		synthers       []sointu.Synther // the synther used to create new synths
		multithreading bool             // is the multithreading enabled or not
		curSynther     sointu.Synther   // the current synther, either multithreaded or not depending on multithreading

		broker *Broker

		midi       midiState
		midiAssign midiAssigns

		buffers        bufferState
		defaultPresets sointu.EncodingPresets

		presetData presetData
	}

	// Cursor identifies a row and a track in a song score.
	Cursor struct {
		Track int
		sointu.SongPos
	}

	// Loop identifier the order rows, which are the loop positions
	// Length = 0 means no loop is chosen, regardless of start
	Loop struct {
		Start, Length int
	}

	Explore struct {
		IsSave       bool         // true if this is a save operation, false if open operation
		IsSong       bool         // true if this is a song, false if instrument
		Continuation func(string) // function to call with the selected file path
	}

	IsPlayingMsg   struct{ bool }
	StartPlayMsg   struct{ sointu.SongPos }
	BPMMsg         struct{ int }
	RowsPerBeatMsg struct{ int }
	PanicMsg       struct{ bool }
	RecordingMsg   struct{ bool }

	ChangeSeverity int
	ChangeType     int

	Dialog int

	InstrumentTab int
)

const (
	MajorChange ChangeSeverity = iota
	MinorChange
)

const (
	NoChange    ChangeType = 0
	PatchChange ChangeType = 1 << iota
	ScoreChange
	BPMChange
	RowsPerBeatChange
	BufferChange
	SongChange ChangeType = PatchChange | ScoreChange | BPMChange | RowsPerBeatChange | BufferChange
)

const (
	NoDialog = iota
	SaveAsExplorer
	NewSongChanges
	NewSongSaveExplorer
	OpenSongChanges
	OpenSongSaveExplorer
	OpenSongOpenExplorer
	Export
	ExportFloatExplorer
	ExportInt16Explorer
	QuitChanges
	QuitSaveExplorer
	License
	DeleteUserPresetDialog
	OverwriteUserPresetDialog
	DeleteModulePresetDialog
	OverwriteModulePresetDialog
)

const (
	InstrumentEditorTab InstrumentTab = iota
	InstrumentPresetsTab
	InstrumentCommentTab
	InstrumentBuffersTab
	InstrumentModulesTab
	NumInstrumentTabs
)

const maxUndo = 64

func (m *Model) Dialog() Dialog { return m.dialog }
func (m *Model) Quitted() bool  { return m.quitted }

// NewModelPlayer creates a new model and a player that communicates with it
func NewModel(broker *Broker, synthers []sointu.Synther, midiContext MIDIContext, recoveryFilePath string) *Model {
	m := new(Model)
	m.synthers = synthers
	m.midi = midiState{context: midiContext}
	m.midiAssign = midiAssigns{ctoi: map[midiAssignKey][]midiAssignRange{}}
	m.broker = broker
	m.d.Octave = 4
	m.linkInstrTrack = true
	m.d.RecoveryFilePath = recoveryFilePath
	m.spectrum = broker.GetSpectrum()
	m.loadEncodingPresets() // before the song is first synced
	m.Song().reset()
	if recoveryFilePath != "" {
		if bytes2, err := os.ReadFile(m.d.RecoveryFilePath); err == nil {
			var data modelData
			if json.Unmarshal(bytes2, &data) == nil {
				m.d = data
			}
		}
	}
	TrySend(broker.ToPlayer, any(m.playerSong())) // we should be non-blocking in the constructor
	m.scopeData = scopeData{lengthInBeats: 4}
	m.Scope().updateBufferLength()
	m.updateDeriveData(SongChange)
	m.presetData.load()
	m.loadModulePresets()
	m.Preset().updateCache()
	m.derived.searchResults = make([]string, 0, len(sointu.UnitNames))
	m.Unit().updateDerivedUnitSearch()
	m.MIDI().Refresh().Do()
	m.Play().setSynther(0, false)
	go runDetector(broker)
	go runSpecAnalyzer(broker)
	go runMIDIHandler(broker)
	go runBufferWorker(broker)
	return m
}

func (m *Model) Close() {
	TrySend(m.broker.CloseDetector, struct{}{})
	TrySend(m.broker.CloseSpecAn, struct{}{})
	TrySend(m.broker.CloseMIDIHandler, struct{}{})
	TrySend(m.broker.CloseBufferWorker, struct{}{})
	TimeoutReceive(m.broker.FinishedDetector, 3*time.Second)
	TimeoutReceive(m.broker.FinishedSpecAn, 3*time.Second)
	TimeoutReceive(m.broker.FinishedMIDIHandler, 3*time.Second)
	TimeoutReceive(m.broker.FinishedBufferWorker, 3*time.Second)
}

// RequestQuit asks the tracker to quit, showing a dialog if there are unsaved
// changes.
func (m *Model) RequestQuit() Action { return MakeAction((*requestQuit)(m)) }

type requestQuit Model

func (m *requestQuit) Do() {
	if !m.quitted {
		m.dialog = QuitChanges
		(*SongModel)(m).completeAction(true)
	}
}

// ForceQuit returns an Action to force the tracker to quit immediately, without
// saving any changes.
func (m *Model) ForceQuit() Action { return MakeAction((*forceQuit)(m)) }

type forceQuit Model

func (m *forceQuit) Do() { m.quitted = true }

// ShowLicense returns an Action to show the software license dialog.
func (m *Model) ShowLicense() Action { return MakeAction((*showLicense)(m)) }

type showLicense Model

func (m *showLicense) Do() { m.dialog = License }

// CancelDialog returns an Action to cancel the current dialog.
func (m *Model) CancelDialog() Action { return MakeAction((*cancelDialog)(m)) }

type cancelDialog Model

func (m *cancelDialog) Do() { m.dialog = NoDialog }

// SetHostSavesState tells the model that a plugin host, e.g. a DAW, saves the
// song as part of its project: onChange is called after each change, and the
// song is not shown as unsaved.
func (m *Model) SetHostSavesState(onChange func()) { m.onChange = onChange }

func (m *Model) notifyChange() {
	if m.onChange != nil {
		m.onChange()
	}
}

func (m *Model) change(kind string, t ChangeType, severity ChangeSeverity) func() {
	if m.changeLevel == 0 {
		m.changeType = NoChange
		m.undoStack = append(m.undoStack, m.d.Copy())
		m.changeCancel = false
		m.changeSeverity = severity
	} else {
		if m.changeSeverity < severity {
			m.changeSeverity = severity
		}
	}
	m.changeType |= t
	m.changeLevel++
	return func() {
		m.changeLevel--
		if m.changeLevel < 0 {
			panic("changeLevel < 0, mismatched change() calls")
		}
		if m.changeLevel == 0 {
			if m.changeCancel || m.d.Song.BPM <= 0 || m.d.Song.RowsPerBeat <= 0 || m.d.Song.Score.Length <= 0 {
				// the change was cancelled or put the song in invalid state, so we don't save it
				m.d = m.undoStack[len(m.undoStack)-1]
				m.undoStack = m.undoStack[:len(m.undoStack)-1]
				// the derived data points to the song that was dropped
				m.updateDeriveData(m.changeType)
				return
			}
			m.d.ChangedSinceSave = true
			m.d.ChangedSinceRecovery = true
			m.notifyChange()
			if m.changeType&ScoreChange != 0 {
				m.d.Cursor.SongPos = m.d.Song.Score.Clamp(m.d.Cursor.SongPos)
				m.d.Cursor2.SongPos = m.d.Song.Score.Clamp(m.d.Cursor2.SongPos)
				TrySend(m.broker.ToPlayer, any(m.d.Song.Score.Copy()))
			}
			if m.changeType&PatchChange != 0 {
				m.fixIDCollisions()
				m.fixUnitParams()
				m.fixModules()
				m.fixSpectrumBuffers()
				m.fixBuses()
				m.d.InstrIndex = clamp(m.d.InstrIndex, 0, len(m.d.Song.Patch)-1)
				m.d.InstrIndex2 = clamp(m.d.InstrIndex2, 0, len(m.d.Song.Patch)-1)
				m.d.ModuleIndex = clamp(m.d.ModuleIndex, 0, len(m.d.Song.Modules)-1)
				m.d.UnitSearching = false // if we change anything in the patch, reset the unit searching
				m.d.UnitSearchString = ""
				m.d.SendSource = 0
				TrySend(m.broker.ToPlayer, any(m.playerSong().Patch))
			}
			m.fixScope() // e.g. the module unit that the cursor was inside was folded
			if m.changeType&BPMChange != 0 {
				TrySend(m.broker.ToPlayer, any(BPMMsg{m.d.Song.BPM}))
				m.Scope().updateBufferLength()
			}
			if m.changeType&RowsPerBeatChange != 0 {
				TrySend(m.broker.ToPlayer, any(RowsPerBeatMsg{m.d.Song.RowsPerBeat}))
			}
			m.updateDeriveData(m.changeType)
			m.undoSkipCounter++
			var limit int
			switch m.changeSeverity {
			default:
			case MajorChange:
				limit = 1
			case MinorChange:
				limit = 10
			}
			if m.prevUndoKind == kind && m.undoSkipCounter < limit {
				m.undoStack = m.undoStack[:len(m.undoStack)-1]
				return
			}
			m.undoSkipCounter = 0
			m.prevUndoKind = kind
			m.redoStack = m.redoStack[:0]
			if len(m.undoStack) > maxUndo {
				copy(m.undoStack, m.undoStack[len(m.undoStack)-maxUndo:])
				m.undoStack = m.undoStack[:maxUndo]
			}
		}
	}
}

func (m *Model) ProcessMsg(msg MsgToModel) {
	if msg.HasPanicPlayerStatus {
		m.playerStatus = msg.PlayerStatus
		if m.playing && m.follow {
			m.d.Cursor.SongPos = msg.PlayerStatus.SongPos
			m.d.Cursor2.SongPos = msg.PlayerStatus.SongPos
			TrySend(m.broker.ToGUI, any(MsgToGUI{
				Kind:  GUIMessageCenterOnRow,
				Param: m.Play().SongRow(),
			}))
		}
		m.panic = msg.Panic
	}
	if msg.HasDetectorResult {
		m.detectorResult = msg.DetectorResult
	}
	if msg.TriggerChannel > 0 {
		m.Scope().trigger(msg.TriggerChannel)
	}
	if msg.Reset {
		m.Scope().reset()
		TrySend(m.broker.ToDetector, MsgToDetector{Reset: true}) // chain the messages: when the signal analyzer is reset, also reset the detector
	}
	switch e := msg.Data.(type) {
	case func():
		e()
	case Recording:
		if e.BPM == 0 {
			e.BPM = float64(m.d.Song.BPM)
		}
		score, err := e.Score(m.d.Song.Patch, m.d.Song.RowsPerBeat, m.d.Song.Score.RowsPerPattern)
		if err != nil || score.Length <= 0 {
			break
		}
		defer m.change("Recording", SongChange, MajorChange)()
		m.d.Song.Score = score
		m.d.Song.BPM = int(e.BPM + 0.5)
		m.trackerHidden = false
	case SpectrumMsg:
		if m.spectra == nil {
			m.spectra = map[SpectrumSource]SpectrumMsg{}
		}
		m.spectra[e.Source] = e
		m.unwatchSpectra()
	case HostBPMMsg:
		if int(e) != m.d.Song.BPM {
			defer m.change("HostBPM", SongChange, MinorChange)()
			m.d.Song.BPM = int(e)
		}
	case Alert:
		m.Alerts().AddAlert(e)
	case IsPlayingMsg:
		m.playing = e.bool
	case *sointu.AudioBuffer:
		m.Scope().processAudioBuffer(e)
		// chain the messages: when we have a new audio buffer, send them to the detector and the spectrum analyzer
		if m.specAnEnabled || m.spectrumWanted() { // send buffers to spectrum analyzer only if it's enabled, or the eq editor shows the spectrum
			clone := m.broker.GetAudioBuffer()
			*clone = append(*clone, *e...)
			if !TrySend(m.broker.ToSpecAn, MsgToSpecAn{Data: clone}) {
				m.broker.PutAudioBuffer(clone)
			}
		}
		if !TrySend(m.broker.ToDetector, MsgToDetector{Data: e}) {
			m.broker.PutAudioBuffer(e)
		}
	case *Spectrum:
		m.broker.PutSpectrum(m.spectrum)
		m.spectrum = e
	case bufferResult:
		m.handleBufferResult(e)
	case *MIDIMessage:
		if channel, control, value, ok := e.getControlChange(); ok {
			m.MIDI().handleControlEvent(channel, int(control), int(value))
		}
	}
}

func (m *Model) Broker() *Broker { return m.broker }

func (d *modelData) Copy() modelData {
	ret := *d
	ret.Song = d.Song.Copy()
	ret.MIDIBindings = d.MIDIBindings.Copy()
	ret.UnitPath = slices.Clone(d.UnitPath)
	return ret
}

func (m *Model) maxID() int {
	maxID := 0
	for units := range m.d.Song.UnitLists() {
		for _, unit := range units {
			if unit.ID > maxID {
				maxID = unit.ID
			}
		}
	}
	return maxID
}

func (m *Model) maxIDandUsed() (maxID int, usedIDs map[int]bool) {
	usedIDs = make(map[int]bool)
	for units := range m.d.Song.UnitLists() {
		for _, unit := range units {
			usedIDs[unit.ID] = true
			if maxID < unit.ID {
				maxID = unit.ID
			}
		}
	}
	return
}

func (m *Model) assignUnitIDsForPatch(patch sointu.Patch) {
	maxId, usedIds := m.maxIDandUsed()
	rewrites := map[int]int{}
	for _, instr := range patch {
		rewriteUnitIds(instr.Units, &maxId, usedIds, rewrites)
	}
	for _, instr := range patch {
		rewriteSendTargets(instr.Units, rewrites)
	}
}

func (m *Model) assignUnitIDs(units []sointu.Unit) {
	maxID, usedIds := m.maxIDandUsed()
	rewrites := map[int]int{}
	rewriteUnitIds(units, &maxID, usedIds, rewrites)
	rewriteSendTargets(units, rewrites)
}

func rewriteUnitIds(units []sointu.Unit, maxId *int, usedIds map[int]bool, rewrites map[int]int) {
	for i := range units {
		if id := units[i].ID; id == 0 || usedIds[id] {
			*maxId++
			if id > 0 {
				rewrites[id] = *maxId
			}
			units[i].ID = *maxId
		}
		usedIds[units[i].ID] = true
		if *maxId < units[i].ID {
			*maxId = units[i].ID
		}
	}
}

func rewriteSendTargets(units []sointu.Unit, rewrites map[int]int) {
	for i := range units {
		if target, ok := units[i].Parameters["target"]; units[i].Type == "send" && ok {
			if newId, ok := rewrites[target]; ok {
				units[i].Parameters["target"] = newId
			}
		}
	}
}

func (m *Model) fixIDCollisions() {
	// loop over all instruments, modules and units and check if two units
	// have the same ID. If so, give the later units new IDs. Units without an
	// ID (0), e.g. in hand-written songs, get one too, without a warning.
	usedIDs := map[int]bool{}
	needsFix, collided := false, false
	maxID := 0
	for units := range m.d.Song.UnitLists() {
		for j, unit := range units {
			if unit.ID == 0 {
				needsFix = true
				continue
			}
			if usedIDs[unit.ID] {
				units[j].ID = 0
				needsFix, collided = true, true
			}
			if unit.ID > maxID {
				maxID = unit.ID
			}
			usedIDs[unit.ID] = true
		}
	}
	if needsFix {
		if collided {
			m.Alerts().AddNamed("IDCollision", "Some units had duplicate IDs, they were fixed", Error)
		}
		for units := range m.d.Song.UnitLists() {
			for j, unit := range units {
				if unit.ID == 0 {
					maxID++
					units[j].ID = maxID
				}
			}
		}
	}
}

var validParameters = map[string](map[string]bool){}

func init() {
	for name, unitType := range sointu.UnitTypes {
		validParameters[name] = map[string]bool{}
		for _, param := range unitType.Params {
			validParameters[name][param.Name] = true
		}
	}
}

func (m *Model) fixUnitParams() {
	// loop over all instruments and units and check that unit parameter table
	// only has the parameters that are defined in the unit type
	fixed := false
	for i := range m.d.Song.Patch {
		fixed = RemoveUnusedUnitParameters(&m.d.Song.Patch[i]) || fixed
	}
	for i := range m.d.Song.Modules {
		fixed = removeUnusedUnitParameters(m.d.Song.Modules[i].Units) || fixed
	}
	if fixed {
		m.Alerts().AddNamed("InvalidUnitParameters", "Some units had invalid parameters, they were removed", Error)
	}
}

// RemoveUnusedUnitParameters removes any parameters from the instrument that are not valid for the unit type.
// It returns true if any parameters were removed.
func RemoveUnusedUnitParameters(instr *sointu.Instrument) bool {
	return removeUnusedUnitParameters(instr.Units)
}

func removeUnusedUnitParameters(units []sointu.Unit) bool {
	fixed := false
	for _, unit := range units {
		for paramName := range unit.Parameters {
			if !validParameters[unit.Type][paramName] {
				delete(unit.Parameters, paramName)
				fixed = true
			}
		}
	}
	return fixed
}

func clamp(a, min, max int) int {
	if a > max {
		return max
	}
	if a < min {
		return min
	}
	return a
}
