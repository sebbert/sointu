package tracker

import (
	"fmt"
	"strings"

	"github.com/vsariola/sointu"
)

// The routing of the output channels between instruments. Instruments run in
// their order, all voices of one before the next: out, outaux and aux add to
// the output channels, in reads a channel and clears it. So an instrument
// that reads a channel is a bus for the instruments before it that write it,
// and one after it that writes the channel is not heard through the bus.

// numPairs is the number of stereo pairs of output channels: main, aux1 to
// aux7.
const numPairs = sointu.NumChannels / 2

// channelUse is which pairs of output channels the units of an instrument
// read with in, and write with out, outaux and aux; looking into modules.
type channelUse struct{ reads, writes [numPairs]bool }

func pairName(p int) string {
	if p == 0 {
		return "main"
	}
	return fmt.Sprintf("aux%d", p)
}

func pairNames(pairs [numPairs]bool) string {
	var names []string
	for p, ok := range pairs {
		if ok {
			names = append(names, pairName(p))
		}
	}
	return strings.Join(names, ", ")
}

func (r *Remote) channelUse(units []sointu.Unit) channelUse {
	var use channelUse
	r.addChannelUse(units, &use, 0)
	return use
}

func (r *Remote) addChannelUse(units []sointu.Unit, use *channelUse, depth int) {
	mark := func(pairs *[numPairs]bool, u *sointu.Unit, channel int) {
		last := channel
		if u.Parameters["stereo"] != 0 {
			last++
		}
		for c := channel; c <= last; c++ {
			if c >= 0 && c < sointu.NumChannels {
				pairs[c/2] = true
			}
		}
	}
	for i := range units {
		u := &units[i]
		if u.Disabled {
			continue
		}
		switch u.Type {
		case "out":
			mark(&use.writes, u, 0)
		case "outaux":
			mark(&use.writes, u, 0)
			mark(&use.writes, u, 2)
		case "aux":
			mark(&use.writes, u, u.Parameters["channel"])
		case "in":
			mark(&use.reads, u, u.Parameters["channel"])
		case "module":
			mods := r.d.Song.Modules
			if k, ok := mods.Find(u.Parameters["module"]); ok && depth <= len(mods) {
				r.addChannelUse(mods[k].Units, use, depth+1)
			}
		}
	}
}

// isBus reports whether an instrument reads a channel with in.
func (r *Remote) isBus(index int) bool {
	return r.channelUse(r.d.Song.Patch[index].Units).reads != [numPairs]bool{}
}

// firstBus is the index of the first instrument that reads a channel, or
// the number of instruments if none does.
func (r *Remote) firstBus() int {
	for i := range r.d.Song.Patch {
		if r.isBus(i) {
			return i
		}
	}
	return len(r.d.Song.Patch)
}

// routingWarnings tells of instruments that write a channel only after the
// last instrument reading it: they are not heard through that bus, e.g. an
// instrument after the master chain bypasses it.
func (r *Remote) routingWarnings() string {
	patch := r.d.Song.Patch
	uses := make([]channelUse, len(patch))
	for i := range patch {
		uses[i] = r.channelUse(patch[i].Units)
	}
	var b strings.Builder
	for i, use := range uses {
		for p := range numPairs {
			if !use.writes[p] {
				continue
			}
			reader, later := -1, false
			for j := range uses {
				if uses[j].reads[p] {
					if j > i {
						later = true
						break
					}
					if j < i {
						reader = j
					}
				}
			}
			if reader >= 0 && !later {
				fmt.Fprintf(&b, "WARNING: instrument %d %q writes %s after instrument %d %q reads it: it bypasses that bus. Move it before (move_instrument), unless that is meant.\n",
					i, patch[i].Name, pairName(p), reader, patch[reader].Name)
			}
		}
	}
	return b.String()
}

// routingNote describes where an instrument sits among the buses, for the
// result of adding or moving it.
func (r *Remote) routingNote(index int) string {
	patch := r.d.Song.Patch
	use := r.channelUse(patch[index].Units)
	var readers []string
	for p := range numPairs {
		if !use.writes[p] {
			continue
		}
		for j := index + 1; j < len(patch); j++ {
			if r.channelUse(patch[j].Units).reads[p] {
				readers = append(readers, fmt.Sprintf("%s goes to instrument %d %q", pairName(p), j, patch[j].Name))
				break
			}
		}
	}
	if len(readers) == 0 {
		return ""
	}
	return "; " + strings.Join(readers, ", ")
}
