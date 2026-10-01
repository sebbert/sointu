package vm

// limiterFrames is the length of the delay line of a limiter, a power of 2:
// more than the longest lookahead, 4·127 samples.
const limiterFrames = 512

// limiterState is the state of a limiter unit in a voice: the peak level it
// follows, the gain reduction (1 - gain, so that a new state passes the
// signal), the frame of the delay line to write next and the delay line of
// the two channels. It does not fit in a unit, so the synths keep the states
// of all limiters in a table of their own, one after the other in the order
// the units run (voice by voice), like those of ott. They are not cleared
// when a note is triggered.
type limiterState struct {
	level, reduction float32
	pos              int32
	_                int32
	line             [limiterFrames][2]float32
}

// limiter limits the channels on top of the stack to the threshold, looking
// ahead by lookahead samples, which is how late its output is. p holds
// threshold, release and drive. Matches $su_op_limiter in the wasm player,
// operation by operation.
func limiter(st *limiterState, p *[8]float32, lookahead int, channels int, stack []float32) {
	l := len(stack)
	drive := 1 + float32(7*p[2])
	line := &st.line[st.pos]
	out := &st.line[(int(st.pos)-lookahead)&(limiterFrames-1)]
	// the peak of what goes into the delay line and of what comes out of
	// it: the level stays up until a peak has come out, and only then falls
	var peak float32
	for i := range channels {
		line[i] = float32(stack[l-1-i] * drive)
		peak = max(peak, abs32(line[i]), abs32(out[i]))
	}
	// the level jumps to a higher peak and falls back by release
	level := max(st.level-float32(st.level*nonLinearMap(p[1])), peak)
	st.level = level
	// the reduction that brings the level down to the threshold, smoothed
	// so that it is nearly there when the peak comes out of the delay line
	var target float32
	if level > p[0] {
		target = 1 - p[0]/level
	}
	st.reduction += float32((target - st.reduction) * (4 / float32(lookahead+4)))
	gain := 1 - st.reduction
	for i := range channels {
		stack[l-1-i] = float32(out[i] * gain)
	}
	st.pos = (st.pos + 1) & (limiterFrames - 1)
}
