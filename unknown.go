package sointu

import (
	"errors"
	"fmt"
)

// UnknownUnit is a unit of a type that this version of Sointu does not have,
// e.g. in a song saved by a newer one: unit Unit of instrument Instrument, or
// if Module is not 0, of the module with that ID. Name is the name of the
// instrument or the module. It is an error that says so.
type UnknownUnit struct {
	Instrument, Module int
	Name               string
	Unit               int
	Type               string
}

func (u UnknownUnit) Error() string {
	where := fmt.Sprintf("instrument %d", u.Instrument)
	if u.Module != 0 {
		where = fmt.Sprintf("module %d", u.Module)
	}
	if u.Name != "" {
		where += " / " + u.Name
	}
	return fmt.Sprintf("unit %d of %s has the unknown type %q", u.Unit, where, u.Type)
}

func unknownUnits(units []Unit, found UnknownUnit, ret []UnknownUnit) []UnknownUnit {
	for j, u := range units {
		if u.Type == "" || u.Disabled {
			continue // not played
		}
		if _, ok := UnitTypes[u.Type]; !ok {
			found.Unit, found.Type = j, u.Type
			ret = append(ret, found)
		}
	}
	return ret
}

// UnknownUnits returns the units of the patch whose type this version of
// Sointu does not have. Disabled units are not played, and not looked at.
func (p Patch) UnknownUnits() (ret []UnknownUnit) {
	for i, instr := range p {
		ret = unknownUnits(instr.Units, UnknownUnit{Instrument: i, Name: instr.Name}, ret)
	}
	return ret
}

// UnknownUnits returns the units of the instruments and the modules of the
// song whose type this version of Sointu does not have.
func (s *Song) UnknownUnits() []UnknownUnit {
	ret := s.Patch.UnknownUnits()
	for _, m := range s.Modules {
		ret = unknownUnits(m.Units, UnknownUnit{Module: m.ID, Name: m.Name}, ret)
	}
	return ret
}

// unknownUnitsError returns the units as one error, or nil.
func unknownUnitsError(units []UnknownUnit) error {
	errs := make([]error, len(units))
	for i, u := range units {
		errs[i] = u
	}
	return errors.Join(errs...)
}

// CheckUnitTypes returns an error naming the units of the song whose type
// this version of Sointu does not have, or nil. The synths and the compiler
// cannot play them.
func (s *Song) CheckUnitTypes() error { return unknownUnitsError(s.UnknownUnits()) }

// WithoutUnknownUnits returns the song without the units whose type this
// version of Sointu does not have, and those units: the song as it can be
// played, less what they did. A song without such units is returned as it
// is, and the units of the other instruments and modules are shared with s.
func (s *Song) WithoutUnknownUnits() (Song, []UnknownUnit) {
	unknown := s.UnknownUnits()
	if len(unknown) == 0 {
		return *s, nil
	}
	strip := func(units []Unit) []Unit {
		ret := make([]Unit, 0, len(units))
		for _, u := range units {
			if _, ok := UnitTypes[u.Type]; ok || u.Type == "" || u.Disabled {
				ret = append(ret, u)
			}
		}
		return ret
	}
	ret := *s
	ret.Patch = append(Patch{}, s.Patch...)
	ret.Modules = append(Modules(nil), s.Modules...)
	for _, u := range unknown {
		if u.Module == 0 {
			ret.Patch[u.Instrument].Units = strip(s.Patch[u.Instrument].Units)
		} else if i, ok := s.Modules.Find(u.Module); ok {
			ret.Modules[i].Units = strip(s.Modules[i].Units)
		}
	}
	return ret, unknown
}
