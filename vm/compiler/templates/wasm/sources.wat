{{- if .HasOp "loadval"}}
;;-------------------------------------------------------------------------------
;;   LOADVAL opcode
;;-------------------------------------------------------------------------------
{{- if .Mono "loadval"}}
;;   Mono: push 2*v-1 on stack, where v is the input to port "value"
{{- end}}
{{- if .Stereo "loadval"}}
;;   Stereo: push 2*v-1 twice on stack
{{- end}}
;;-------------------------------------------------------------------------------
(func $su_op_loadval (param $stereo i32)
{{- if .Stereo "loadval"}}
    (if (local.get $stereo) (then
        (call $su_op_loadval (i32.const 0))
    ))
{{- end}}
    (f32.sub (call $input (i32.const {{.InputNumber "loadval" "value"}})) (f32.const 0.5))
    (f32.mul (f32.const 2.0))
    (call $push)
)
{{end}}


{{if .HasOp "envelope" -}}
;;-------------------------------------------------------------------------------
;;   ENVELOPE opcode: pushes an ADSR envelope value on stack [0,1]
;;-------------------------------------------------------------------------------
;;   Mono:   push the envelope value on stack
;;   Stereo: push the envelope valeu on stack twice
;;-------------------------------------------------------------------------------
(func $su_op_envelope (param $stereo i32) (local $state i32) (local $level f32) (local $delta f32)
{{- if .EnvelopeCurve}} (local $curve f32){{end}}
    (if (i32.eqz (i32.load offset=4 (global.get $voice))) (then ;; if voice.sustain == 0
{{- if .EnvelopeCurve}}
        (if (i32.ne (i32.load (global.get $WRK)) (i32.const {{.InputNumber "envelope" "release"}})) (then
            (f32.store offset=8 (global.get $WRK) (f32.load offset=4 (global.get $WRK))) ;; the level where the release starts
        ))
{{- end}}
        (i32.store (global.get $WRK) (i32.const {{.InputNumber "envelope" "release"}})) ;; set envelope state to release
    ))
    (local.set $state (i32.load (global.get $WRK)))
    (local.set $level (f32.load offset=4 (global.get $WRK)))
    (local.set $delta (call $nonLinearMap (local.get $state)))
{{- if .EnvelopeCurve}}
    (local.set $curve (f32.mul (f32.mul (call $input (i32.const {{.InputNumber "envelope" "curve"}})) (call $input (i32.const {{.InputNumber "envelope" "curve"}}))) (f32.const 12)))
{{- end}}
    (if (local.get $state) (then
        (if (i32.eq (local.get $state) (i32.const 1))(then ;; state is 1 aka decay
{{- if .EnvelopeCurve}}
            (local.set $level (call $envelopeStep (local.get $level) (f32.neg (local.get $delta)) (f32.const 1) (call $input (i32.const 2)) (local.get $curve)))
{{- else}}
            (local.set $level (f32.sub (local.get $level) (local.get $delta)))
{{- end}}
            (if (f32.le (local.get $level) (call $input (i32.const 2)))(then
                (local.set $level (call $input (i32.const 2)))
                (local.set $state (i32.const {{.InputNumber "envelope" "sustain"}}))
            ))
        ))
        (if (i32.eq (local.get $state) (i32.const {{.InputNumber "envelope" "release"}}))(then ;; state is 3 aka release
{{- if .EnvelopeCurve}}
            (local.set $level (call $envelopeStep (local.get $level) (f32.neg (local.get $delta)) (f32.load offset=8 (global.get $WRK)) (f32.const 0) (local.get $curve)))
{{- else}}
            (local.set $level (f32.sub (local.get $level) (local.get $delta)))
{{- end}}
            (if (f32.le (local.get $level) (f32.const 0)) (then
                (local.set $level (f32.const 0))
            ))
        ))
    )(else ;; the state is 0 aka attack
{{- if .EnvelopeCurve}}
        (local.set $level (call $envelopeStep (local.get $level) (local.get $delta) (f32.const 0) (f32.const 1) (local.get $curve)))
{{- else}}
        (local.set $level (f32.add (local.get $level) (local.get $delta)))
{{- end}}
        (if (f32.ge (local.get $level) (f32.const 1))(then
            (local.set $level (f32.const 1))
            (local.set $state (i32.const 1))
        ))
    ))
    (i32.store (global.get $WRK) (local.get $state))
    (f32.store offset=4 (global.get $WRK) (local.get $level))
    (call $push (f32.mul (local.get $level) (call $input (i32.const {{.InputNumber "envelope" "gain"}}))))
{{- if .Stereo "envelope"}}
    (if (local.get $stereo)(then
        (call $push (call $peek))
    ))
{{- end}}
)
{{- if .EnvelopeCurve}}

;; $envelopeStep moves the $level of an envelope stage from $start to $end by
;; one sample, as envelopeStep in vm/go_synth.go: linearly by $delta when
;; $curve is below 2^-20, otherwise with a one-pole filter toward a target
;; beyond $end, end + (end-start)/(2^curve-1), so that the stage takes as long
;; as the linear one.
(func $envelopeStep (param $level f32) (param $delta f32) (param $start f32) (param $end f32) (param $curve f32) (result f32)
    (if (result f32) (f32.lt (local.get $curve) (f32.const 0x1p-20)) (then
        (f32.add (local.get $level) (local.get $delta))
    )(else
        (f32.add
            (local.get $level)
            (f32.mul
                (f32.sub
                    (local.get $level)
                    (f32.add (f32.div (f32.sub (local.get $end) (local.get $start)) (call $exp2m1f (local.get $curve))) (local.get $end)))
                (call $exp2m1f (f32.div (f32.mul (f32.neg (local.get $curve)) (f32.abs (local.get $delta))) (f32.abs (f32.sub (local.get $end) (local.get $start)))))))
    ))
)
{{- end}}
{{end}}


{{- if .HasOp "noise"}}
;;-------------------------------------------------------------------------------
;;   NOISE opcode: creates noise
;;-------------------------------------------------------------------------------
;;   Mono:   push a random value [-1,1] value on stack
;;   Stereo: push two (different) random values on stack
;;-------------------------------------------------------------------------------
(func $su_op_noise (param $stereo i32)
{{- if .Stereo "noise" }}
    (if (local.get $stereo) (then
        (call $su_op_noise (i32.const 0))
    ))
{{- end}}
    (global.set $randseed (i32.mul (global.get $randseed) (i32.const 16007)))
    (f32.mul
        (call $waveshaper
            ;; Note: in x86 code, the constant looks like a positive integer, but has actually the MSB set i.e. is considered negative by the FPU. This tripped me big time.
            (f32.div (f32.convert_i32_s (global.get $randseed)) (f32.const -2147483648))
            (call $input (i32.const {{.InputNumber "noise" "shape"}}))
        )
        (call $input (i32.const {{.InputNumber "noise" "gain"}}))
    )
    (call $push)
)
{{end}}


{{- if .HasOp "oscillator"}}
;;-------------------------------------------------------------------------------
;;   OSCILLAT opcode: oscillator, the heart of the synth
;;-------------------------------------------------------------------------------
;;   Mono:   push oscillator value on stack
;;   Stereo: push l r on stack, where l has opposite detune compared to r
;;-------------------------------------------------------------------------------
(func $su_op_oscillator (param $stereo i32) (local $flags i32) (local $detune f32) (local $phase f32) (local $color f32) (local $amplitude f32)
{{- if .SupportsParamValueOtherThan "oscillator" "unison" 0}}
    (local $unison i32) (local $WRK_stash i32) (local $detune_stash f32)
{{- end}}
{{- if .SupportsModulation "oscillator" "frequency"}}
    (local $freqMod f32)
{{- end}}
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
    (local $dt f32) (local $dphase f32)
{{- end}}
{{- if .Stereo "oscillator"}}
    (local $WRK_stereostash i32)
    (local.set $WRK_stereostash (global.get $WRK))
{{- end}}
{{- if .SupportsModulation "oscillator" "frequency"}}
    (local.set $freqMod (f32.load offset={{.InputNumber "oscillator" "frequency" | mul 4 | add 32}} (global.get $WRK)))
    (f32.store offset={{.InputNumber "oscillator" "frequency" | mul 4 | add 32}} (global.get $WRK) (f32.const 0))
{{- end}}
    (local.set $flags (call $scanOperand))
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
    ;; the phase parameter of the previous sample is in port 7, which the
    ;; oscillator has no input for
    (local.set $dphase (f32.sub
        (call $input (i32.const {{.InputNumber "oscillator" "phase"}}))
        (f32.load offset=60 (global.get $WRK))
    ))
    (f32.store offset=60 (global.get $WRK) (call $input (i32.const {{.InputNumber "oscillator" "phase"}})))
{{- end}}
    (local.set $detune (call $inputSigned (i32.const {{.InputNumber "oscillator" "detune"}})))
{{- if .Stereo "oscillator"}}
    loop $stereoLoop
{{- end}}
{{- if .SupportsParamValueOtherThan "oscillator" "unison" 0}}
    (local.set $unison (i32.add (i32.and (local.get $flags) (i32.const 3)) (i32.const 1)))
    (local.set $WRK_stash (global.get $WRK))
    (local.set $detune_stash (local.get $detune))
    (call $push (f32.const 0))
    loop $unisonLoop
{{- end}}
    (f32.store ;; update phase
        (global.get $WRK)
        (local.tee $phase
            (f32.sub
                (local.tee $phase
                    ;; Transpose calculation starts
                    (f32.div
                        (call $inputSigned (i32.const {{.InputNumber "oscillator" "transpose"}}))
                        (f32.const 0.015625)
                    ) ;; scale back to 0 - 128
                    (f32.add (local.get $detune)) ;; add detune. detune is -1 to 1 so can detune a full note up or down at max
                    (f32.add (select
                        (f32.const 0)
                        (f32.convert_i32_u (i32.load (global.get $voice)))
                        (i32.and (local.get $flags) (i32.const 0x8))
                    ))  ;; if lfo is not enabled, add the note number to it
                    (f32.mul (f32.const 0.0833333)) ;; /12, in full octaves
                    (call $pow2)
                    (f32.mul (select
                        (f32.const 0.000038) ;; pretty random scaling constant to get LFOs into reasonable range. Historical reasons, goes all the way back to 4klang
                        (f32.const 0.000092696138) ;; scaling constant to get middle-C to where it should be
                        (i32.and (local.get $flags) (i32.const 0x8))
                    ))
{{- if .SupportsModulation "oscillator" "frequency"}}
                    (f32.add (local.get $freqMod))
{{- end}}
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
                    (local.tee $dt) ;; the phase advance, without the phase parameter
{{- end}}
                    (f32.add (f32.load (global.get $WRK))) ;; add the current phase of the oscillator
                )
                (f32.floor (local.get $phase))
            )
        )
    )
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
    ;; gate (0x04) with a waveform bit means bandlimited; dt is then the phase
    ;; advance including the phase parameter, and 0 otherwise
    (local.set $dt (select
        (f32.min (f32.max (f32.abs (f32.add (local.get $dt) (local.get $dphase))) (f32.const 9.5367431640625e-7)) (f32.const 0.5))
        (f32.const 0)
        (i32.and (local.get $flags) (i32.const 0x04))
    ))
{{- end}}
    (f32.add (local.get $phase) (call $input (i32.const {{.InputNumber "oscillator" "phase"}})))
    (local.set $phase (f32.sub (local.tee $phase) (f32.floor (local.get $phase)))) ;; phase = phase mod 1.0
    (local.set $color (call $input (i32.const {{.InputNumber "oscillator" "color"}})))
{{- if .SupportsParamValue "oscillator" "type" .Sine}}
    (if (i32.and (local.get $flags) (i32.const 0x40)) (then
        (local.set $amplitude (call $oscillator_sine (local.get $phase) (local.get $color){{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (local.get $dt){{end}}))
    ))
{{- end}}
{{- if .SupportsParamValue "oscillator" "type" .Trisaw}}
    (if (i32.and (local.get $flags) (i32.const 0x20)) (then
        (local.set $amplitude (call $oscillator_trisaw (local.get $phase) (local.get $color){{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (local.get $dt){{end}}))
    ))
{{- end}}
{{- if .SupportsParamValue "oscillator" "type" .Pulse}}
    (if (i32.and (local.get $flags) (i32.const 0x10)) (then
        (local.set $amplitude (call $oscillator_pulse (local.get $phase) (local.get $color){{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (local.get $dt){{end}}))
    ))
{{- end}}
{{- if .SupportsParamValue "oscillator" "type" .Gate}}
    (if {{if .SupportsParamValue "oscillator" "bandlimit" 1}}(i32.eq (i32.and (local.get $flags) (i32.const 0x74)) (i32.const 0x04)){{else}}(i32.and (local.get $flags) (i32.const 0x04)){{end}} (then
        (local.set $amplitude (call $oscillator_gate (local.get $phase)))
        ;; wave shaping is skipped with gate
    )(else
        (local.set $amplitude (call $waveshaper (local.get $amplitude) (call $input (i32.const {{.InputNumber "oscillator" "shape"}}))))
    ))
    (local.get $amplitude)
{{- else}}
    (call $waveshaper (local.get $amplitude) (call $input (i32.const {{.InputNumber "oscillator" "shape"}})))
{{- end}}
    (call $push (f32.mul
        (call $input (i32.const {{.InputNumber "oscillator" "gain"}}))
    ))
{{- if .SupportsParamValueOtherThan "oscillator" "unison" 0}}
    (call $push (f32.add (call $pop) (call $pop)))
    (if (local.tee $unison (i32.sub (local.get $unison) (i32.const 1)))(then
        (f32.store offset={{.InputNumber "oscillator" "phase" | mul 4 | add (index .Labels "su_transformedoperands")}} (i32.const 0)
            (f32.add
                (call $input (i32.const {{.InputNumber "oscillator" "phase"}}))
                (f32.const 0.08333333) ;; 1/12, add small phase shift so all oscillators don't start in phase
            )
        )
        (global.set $WRK (i32.add (global.get $WRK) (i32.const 8)))
        (local.set $detune (f32.neg (f32.mul
            (local.get $detune) ;; each unison oscillator has a detune with flipped sign and halved amount... this creates detunes that concentrate around the fundamental
            (f32.const 0.5)
        )))
        br $unisonLoop
    ))
    end
    (global.set $WRK (local.get $WRK_stash))
    (local.set $detune (local.get $detune_stash))
{{- end}}
{{- if .Stereo "oscillator"}}
    (local.set $detune (f32.neg (local.get $detune))) ;; flip the detune for secon round
    (global.set $WRK (i32.add (global.get $WRK) (i32.const 4)))
    (br_if $stereoLoop (i32.eqz (local.tee $stereo (i32.eqz (local.get $stereo)))))
    end
    (global.set $WRK (local.get $WRK_stereostash))
    ;; TODO: all this "save WRK to local variable, modify it and then restore it" could be better thought out
    ;; however, it is now done like this as a quick bug fix to the issue of stereo oscillators touching WRK and not restoring it
{{- end}}
)

{{- if .SupportsParamValue "oscillator" "type" .Pulse}}
(func $oscillator_pulse (param $phase f32) (param $color f32){{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (param $dt f32){{end}} (result f32)
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (local $amplitude f32)
    (if (f32.gt (local.get $dt) (f32.const 0)) (then
        (local.set $color (f32.min (f32.max (local.get $color) (f32.const 0)) (f32.const 1)))
    ))
    (local.set $amplitude
{{- end}}
    (select
        (f32.const -1)
        (f32.const 1)
        (f32.ge (local.get $phase) (local.get $color))
    )
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
    )
    (if (f32.gt (local.get $dt) (f32.const 0)) (then
        (local.set $amplitude (f32.sub
            (f32.add (local.get $amplitude) (call $polyblep (local.get $phase) (local.get $dt)))
            (call $polyblep (call $wrap (f32.sub (local.get $phase) (local.get $color))) (local.get $dt))
        ))
    ))
    (local.get $amplitude)
{{- end}}
)
{{end}}

{{- if .SupportsParamValue "oscillator" "type" .Sine}}
(func $oscillator_sine (param $phase f32) (param $color f32){{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (param $dt f32){{end}} (result f32)
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (local $h f32)
    (if (f32.gt (local.get $dt) (f32.const 0)) (then
        (local.set $color (f32.min (f32.max (local.get $color) (local.get $dt)) (f32.const 1)))
        ;; the slope changes by ±2π/color at 0 and color
        (local.set $h (f32.mul
            (f32.div (f32.mul (local.get $dt) (f32.const 1.0471976)) (local.get $color))
            (f32.sub
                (call $polyblamp (local.get $phase) (local.get $dt))
                (call $polyblamp (call $wrap (f32.sub (local.get $phase) (local.get $color))) (local.get $dt))
            )
        ))
    ))
{{- end}}
    (select
        (f32.const 0)
{{- if .MathImports}}
        (call $sin (f32.mul
            (f32.div
                (local.get $phase)
                (local.get $color)
            )
            (f32.const 6.28318530718)
        ))
{{- else}}
        (call $sinTurns (f32.div (local.get $phase) (local.get $color)))
{{- end}}
        (f32.ge (local.get $phase) (local.get $color))
    )
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
    (f32.add (local.get $h))
{{- end}}
)
{{end}}

{{- if .SupportsParamValue "oscillator" "type" .Trisaw}}
(func $oscillator_trisaw (param $phase f32) (param $color f32){{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (param $dt f32){{end}} (result f32)
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}} (local $h f32)
    (if (f32.gt (local.get $dt) (f32.const 0)) (then
        (local.set $color (f32.min (f32.max (local.get $color) (local.get $dt)) (f32.sub (f32.const 1) (local.get $dt))))
        ;; the slope changes by ±2/(color·(1-color)) at 0 and color
        (local.set $h (f32.mul
            (f32.div
                (f32.mul (local.get $dt) (f32.const 0.33333334))
                (f32.mul (local.get $color) (f32.sub (f32.const 1) (local.get $color)))
            )
            (f32.sub
                (call $polyblamp (local.get $phase) (local.get $dt))
                (call $polyblamp (call $wrap (f32.sub (local.get $phase) (local.get $color))) (local.get $dt))
            )
        ))
    ))
{{- end}}
    (if (f32.ge (local.get $phase) (local.get $color)) (then
        (local.set $phase (f32.sub (f32.const 1) (local.get $phase)))
        (local.set $color (f32.sub (f32.const 1) (local.get $color)))
    ))
    (f32.div (local.get $phase) (local.get $color))
    (f32.mul (f32.const 2))
    (f32.sub (f32.const 1))
{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
    (f32.add (local.get $h))
{{- end}}
)
{{end}}

{{- if .SupportsParamValue "oscillator" "bandlimit" 1}}
;; The corrections of the bandlimited oscillators for a discontinuity at phase
;; 0, as in vm/go_synth.go. $polywindow is 1 - |d|/dt within dt of it, where d
;; is the distance to it, and 0 further away.
(func $polywindow (param $t f32) (param $dt f32) (result f32)
    (f32.max
        (f32.sub (f32.const 1) (f32.div
            (f32.min (local.get $t) (f32.sub (f32.const 1) (local.get $t)))
            (local.get $dt)
        ))
        (f32.const 0)
    )
)

;; $polyblep corrects a step of +2: -(1-d/dt)² after it, (1-d/dt)² before
(func $polyblep (param $t f32) (param $dt f32) (result f32) (local $y f32)
    (local.set $y (call $polywindow (local.get $t) (local.get $dt)))
    (f32.copysign
        (f32.mul (local.get $y) (local.get $y))
        (f32.sub (local.get $t) (f32.const 0.5))
    )
)

;; $polyblamp is 6/dt times the correction for a corner where the slope
;; increases by 1: (1-|d|/dt)³
(func $polyblamp (param $t f32) (param $dt f32) (result f32) (local $y f32)
    (local.set $y (call $polywindow (local.get $t) (local.get $dt)))
    (f32.mul (f32.mul (local.get $y) (local.get $y)) (local.get $y))
)

(func $wrap (param $x f32) (result f32)
    (f32.sub (local.get $x) (f32.floor (local.get $x)))
)
{{end}}

{{- if .SupportsParamValue "oscillator" "type" .Gate}}
(func $oscillator_gate (param $phase f32) (result f32) (local $x f32)
    (f32.store offset=16 (global.get $WRK)
        (local.tee $x
            (f32.add ;; c*(g-x)+x
                (f32.mul ;; c*(g-x)
                    (f32.sub ;; g - x
                        (f32.load offset=16 (global.get $WRK)) ;; g
                        (local.tee $x
                            (f32.convert_i32_u ;; 'x' gate bit = float((gatebits >> (int(p*16+.5)&15)) & 1)
                                (i32.and ;; (int(p*16+.5)&15)&1
                                    (i32.shr_u ;; int(p*16+.5)&15
                                        (i32.load16_u (i32.sub (global.get $VAL) (i32.const 4)))
                                        (i32.and ;; int(p*16+.5) & 15
                                            (i32.trunc_f32_s (f32.add
                                                (f32.mul
                                                    (local.get $phase)
                                                    (f32.const 16.0)
                                                )
                                                (f32.const 0.5) ;; well, x86 rounds to integer by default; on wasm, we have only trunc.
                                            ))                  ;; This is just for rendering similar to x86, should probably delete when optimizing size.
                                            (i32.const 15)
                                        )
                                    )
                                    (i32.const 1)
                                )
                            )
                        )
                    )
                    (f32.const 0.99609375) ;; 'c'
                )
                (local.get $x)
            )
        )
    )
    local.get $x
)
{{end}}

{{end}}


{{- if .HasOp "receive"}}
;;-------------------------------------------------------------------------------
;;   RECEIVE opcode
;;-------------------------------------------------------------------------------
{{- if .Mono "receive"}}
;;   Mono:   push l on stack, where l is the left channel received
{{- end}}
{{- if .Stereo "receive"}}
;;   Stereo: push l r on stack
{{- end}}
;;-------------------------------------------------------------------------------
(func $su_op_receive (param $stereo i32)
{{- if .Stereo "receive"}}
    (if (local.get $stereo) (then
        (call $push
            (f32.load offset=36 (global.get $WRK))
        )
        (f32.store offset=36 (global.get $WRK) (f32.const 0))
    ))
{{- end}}
    (call $push
        (f32.load offset=32 (global.get $WRK))
    )
    (f32.store offset=32 (global.get $WRK) (f32.const 0))
)
{{end}}


{{- if .HasOp "in"}}
;;-------------------------------------------------------------------------------
;;   IN opcode: inputs and clears a global port
;;-------------------------------------------------------------------------------
;;   Mono: push the left channel of a global port (out or aux)
;;   Stereo: also push the right channel (stack in l r order)
;;-------------------------------------------------------------------------------
(func $su_op_in (param $stereo i32) (local $addr i32)
    call $scanOperand
{{- if .Stereo "in"}}
    (i32.add (local.get $stereo)) ;; start from right channel if stereo
{{- end}}
    (local.set $addr (i32.add (i32.mul (i32.const 4)) (i32.const {{index .Labels "su_globalports"}})))
{{- if .Stereo "in"}}
    loop $stereoLoop
{{- end}}
        (call $push (f32.load (local.get $addr)))
        (f32.store (local.get $addr) (f32.const 0))
{{- if .Stereo "in"}}
        (local.set $addr (i32.sub (local.get $addr) (i32.const 4)))
        (br_if $stereoLoop (i32.eqz (local.tee $stereo (i32.eqz (local.get $stereo)))))
    end
{{- end}}
)
{{end}}


{{- if or .SpawnLength .WindowOwnLength}}
;; $lengthFrames returns the length in frames of a spawn or window unit for its
;; length parameter: 100 ms at the middle, doubling every 8 steps
(func $lengthFrames (param $length f32) (result f32)
    (f32.floor (f32.mul
        (f32.const 4410)
        (call $pow2 (f32.sub (f32.mul (local.get $length) (f32.const 16)) (f32.const 8)))
    ))
)
{{end}}

{{- if .HasOp "window"}}
;;-------------------------------------------------------------------------------
;;   WINDOW opcode: push a window over the note of the voice
;;-------------------------------------------------------------------------------
;;   WRK[0] is the number of frames since the note was triggered. Length 0
;;   takes the length of the spawned note from offset 32 of the voice.
;;   Matches window in vm/go_synth.go.
;;-------------------------------------------------------------------------------
(func $su_op_window (param $stereo i32) (local $age i32) (local $frames f32) (local $t f32) (local $half f32) (local $d f32) (local $x f32)
    (if (i32.eqz (i32.load (global.get $voice))) (then
        (call $push (f32.const 0))
        return
    ))
    (local.set $age (i32.load (global.get $WRK)))
    (i32.store (global.get $WRK) (i32.add (local.get $age) (i32.const 1)))
{{- if .WindowNoteLength}}
    (local.set $frames (f32.convert_i32_u (i32.load offset=32 (global.get $voice)))) ;; the length of the spawned note
{{- end}}
{{- if .WindowOwnLength}}
{{- if .WindowNoteLength}}
    (if (f32.gt (call $input (i32.const {{.InputNumber "window" "length"}})) (f32.const 0)) (then
{{- end}}
        (local.set $frames (f32.max (call $lengthFrames (call $input (i32.const {{.InputNumber "window" "length"}}))) (f32.const 1)))
{{- if .WindowNoteLength}}
    ))
{{- end}}
{{- end}}
{{- if .WindowNoteLength}}
    (if (f32.eq (local.get $frames) (f32.const 0)) (then
        (call $push (f32.const 1)) ;; a note without a length
        return
    ))
{{- end}}
    (local.set $t (f32.div (f32.convert_i32_u (local.get $age)) (local.get $frames)))
    (if (f32.ge (local.get $t) (f32.const 1)) (then
        (call $push (f32.const 0))
        return
    ))
    (local.set $half (f32.mul
{{- if .SupportsModulation "window" "shape"}}
        (f32.min (f32.max (call $input (i32.const {{.InputNumber "window" "shape"}})) (f32.const 0)) (f32.const 1))
{{- else}}
        (call $input (i32.const {{.InputNumber "window" "shape"}})) ;; not modulated: within 0 and 1
{{- end}}
        (f32.const 0.5)
    ))
    (local.set $d (f32.min (local.get $t) (f32.sub (f32.const 1) (local.get $t))))
    (if (f32.ge (local.get $d) (local.get $half)) (then
        (call $push (f32.const 1))
        return
    ))
    (local.set $x (f32.div (local.get $d) (local.get $half)))
    (call $push (f32.mul
        (f32.mul (local.get $x) (local.get $x))
        (f32.sub (f32.const 3) (f32.mul (f32.const 2) (local.get $x)))
    ))
)
{{end}}

{{- if .HasOp "arg"}}
;;-------------------------------------------------------------------------------
;;   ARG opcode: push a value passed by the spawn unit that triggered the voice
;;-------------------------------------------------------------------------------
(func $su_op_arg (param $stereo i32)
    (call $push (f32.load offset=16 (i32.add
        (global.get $voice)
        (i32.shl (call $scanOperand) (i32.const 2))
    )))
)
{{end}}

{{- if .HasOp "bufread"}}
;;-------------------------------------------------------------------------------
;;   BUFREAD opcode: plays a region of a buffer
;;-------------------------------------------------------------------------------
;;   Mono:   push the next frame of the buffer (channels mixed) on stack
;;   Stereo: push r l on stack
;;   Positions are in frames from the oldest valid frame of the buffer when
;;   the note was triggered. WRK[0] is the integer part of the position
;;   (signed), WRK[1] the fraction, WRK[2] the oldest valid frame at the
;;   trigger, WRK[3] 1 once playback has started, WRK[4] 1 once the position
;;   has been in the loop and WRK[5] the filled length at the trigger. Voices never triggered (note 0) are silent. Matches
;;   bufread in vm/go_synth.go.
;;-------------------------------------------------------------------------------
{{- $fadeArgs := ""}}
{{- if .BufreadFade}}{{$fadeArgs = "(local.get $ll) (local.get $le) (local.get $fade) "}}{{end}}
(func $su_op_bufread (param $stereo i32) (local $r i32) (local $h i32) (local $cap i32) (local $filled i32) (local $pos i32) (local $frac f32) (local $base i32) (local $next i32) (local $ls i32) (local $ll i32) (local $le i32) (local $fade i32) (local $inloop i32) (local $start i32) (local $g f32) (local $rel i32) (local $semitones f32) (local $whole f32)
    (local.set $r (i32.add (i32.const {{index .Labels "su_buffer_regions"}}) (i32.mul (call $scanOperand) (i32.const 28))))
    (local.set $h (i32.add (i32.const {{index .Labels "su_buffer_headers"}}) (i32.load (local.get $r))))
    (local.set $cap (i32.load offset=4 (local.get $h)))
    (local.set $filled (i32.load offset=16 (local.get $h)))
    (if (i32.or (i32.eqz (i32.load (global.get $voice))) (i32.eqz (local.get $cap))) (then
{{- if .BufreadMod}}
        (call $bufreadClearPorts)
{{- end}}
{{- if .Stereo "bufread"}}
        (if (local.get $stereo) (then (call $push (f32.const 0))))
{{- end}}
        (call $push (f32.const 0))
        return
    ))
    (local.set $pos (i32.load (global.get $WRK)))
    (local.set $frac (f32.load offset=4 (global.get $WRK)))
    (local.set $base (i32.load offset=8 (global.get $WRK)))
{{- if .BufreadBackwards}}
    (local.set $inloop (i32.load offset=16 (global.get $WRK)))
{{- end}}
    (if (i32.eqz (i32.load offset=12 (global.get $WRK))) (then
        (local.set $base (call $bufreadOldest (local.get $h)))
{{- if .BufreadNegLoopStart}}
        (i32.store offset=20 (global.get $WRK) (local.get $filled)) ;; for negative loop starts
{{- end}}
        (local.set $start (i32.load offset=4 (local.get $r)))
{{- if .BufreadNegStart}}
        (if (i32.lt_s (local.get $start) (i32.const 0)) (then ;; from the newest frame
            (local.set $start (i32.add (local.get $start) (local.get $filled)))
        ))
{{- end}}
{{- if .BufreadMod}}
        (local.set $pos (call $bufreadFrames (local.get $start) (f32.load offset={{add 32 (mul 4 (.InputNumber "bufread" "start"))}} (global.get $WRK)) (local.get $filled) (local.get $cap)))
{{- else}}
        (local.set $pos (call $bufreadClamp (local.get $start) (local.get $cap)))
{{- end}}
{{- if .BufreadModStart}}
        (local.set $frac (f32.mul (f32.load offset={{add 32 (mul 4 (.InputNumber "bufread" "start"))}} (global.get $WRK)) (f32.convert_i32_u (local.get $filled))))
        (local.set $frac (f32.sub (local.get $frac) (f32.floor (local.get $frac))))
{{- end}}
    ))
    (local.set $next (i32.add (local.get $pos) (i32.const 1)))
{{- if .BufreadLoop}}
    (if (i32.and (i32.load offset=24 (local.get $r)) (i32.const 2)) (then ;; loop
        (local.set $ls (i32.load offset=8 (local.get $r)))
{{- if .BufreadNegLoopStart}}
        (if (i32.lt_s (local.get $ls) (i32.const 0)) (then ;; from the newest frame at the trigger
            (local.set $ls (i32.add (local.get $ls) (i32.load offset=20 (global.get $WRK))))
        ))
{{- end}}
{{- if .BufreadMod}}
        (local.set $ls (call $bufreadFrames (local.get $ls) (f32.load offset={{add 32 (mul 4 (.InputNumber "bufread" "loopstart"))}} (global.get $WRK)) (local.get $filled) (local.get $cap)))
        (local.set $ll (call $bufreadFrames (i32.load offset=12 (local.get $r)) (f32.load offset={{add 32 (mul 4 (.InputNumber "bufread" "looplength"))}} (global.get $WRK)) (local.get $filled) (local.get $cap)))
{{- else}}
        (local.set $ls (call $bufreadClamp (local.get $ls) (local.get $cap)))
        (local.set $ll (call $bufreadClamp (i32.load offset=12 (local.get $r)) (local.get $cap)))
{{- end}}
        (local.set $le (i32.add (local.get $ls) (local.get $ll)))
{{- if .BufreadFade}}
        (local.set $fade (call $minu (call $minu (i32.load offset=16 (local.get $r)) (local.get $ls)) (local.get $ll)))
{{- end}}
        (if (local.get $ll) (then
            (if (i32.ge_s (local.get $pos) (local.get $le)) (then
                (local.set $pos (i32.add (local.get $ls) (i32.rem_u (i32.sub (local.get $pos) (local.get $ls)) (local.get $ll))))
{{- if .BufreadBackwards}}
            )(else
                (if (i32.and (local.get $inloop) (i32.lt_s (local.get $pos) (local.get $ls))) (then ;; backwards past the loop start
                    (local.set $pos (i32.sub
                        (i32.sub (local.get $le) (i32.const 1))
                        (i32.rem_u (i32.sub (i32.sub (local.get $ls) (i32.const 1)) (local.get $pos)) (local.get $ll))
                    ))
                ))
            ))
            (if (i32.ge_s (local.get $pos) (local.get $ls)) (then (local.set $inloop (i32.const 1))))
{{- else}}
            ))
{{- end}}
            (local.set $next (i32.add (local.get $pos) (i32.const 1)))
            (if (i32.ge_s (local.get $next) (local.get $le)) (then
                (local.set $next (local.get $ls))
            ))
        ))
    ))
{{- end}}
{{- if .BufreadMod}}
    (call $bufreadClearPorts)
{{- end}}
    (local.set $g (call $input (i32.const {{.InputNumber "bufread" "gain"}})))
{{- if .BufreadEdgeFade}}
    ;; fade out near the edges of the valid frames
    (if (i32.and
            (i32.ne (i32.load offset=20 (local.get $r)) (i32.const 0))
            (i32.lt_u (local.get $pos) (local.get $cap))) (then ;; unsigned: also not negative
        (local.set $rel (i32.rem_u
            (i32.sub
                (i32.add (i32.rem_u (i32.add (local.get $base) (local.get $pos)) (local.get $cap)) (local.get $cap))
                (call $bufreadOldest (local.get $h)))
            (local.get $cap)))
        (if (i32.lt_u (local.get $rel) (local.get $filled)) (then
            (local.set $g (f32.mul (local.get $g) (f32.min
                (f32.div
                    (f32.convert_i32_u (call $minu (local.get $rel) (i32.sub (i32.sub (local.get $filled) (i32.const 1)) (local.get $rel))))
                    (f32.convert_i32_u (i32.load offset=20 (local.get $r))))
                (f32.const 1))))
        ))
    ))
{{- end}}
{{- if .Stereo "bufread"}}
    (if (local.get $stereo) (then
        (call $push (call $bufreadRead (local.get $h) (local.get $base) (local.get $pos) (local.get $next) (local.get $frac) {{$fadeArgs}}(i32.const 1) (local.get $g)))
        (call $push (call $bufreadRead (local.get $h) (local.get $base) (local.get $pos) (local.get $next) (local.get $frac) {{$fadeArgs}}(i32.const 0) (local.get $g)))
    )(else
{{- end}}
{{- if .BufreadMonoMix}}
{{- if .BufreadMonoPlain}}
    (if (i32.eq (i32.load offset=8 (local.get $h)) (i32.const 2)) (then
{{- end}}
        (call $push (f32.mul
            (f32.add
                (call $bufreadRead (local.get $h) (local.get $base) (local.get $pos) (local.get $next) (local.get $frac) {{$fadeArgs}}(i32.const 0) (local.get $g))
                (call $bufreadRead (local.get $h) (local.get $base) (local.get $pos) (local.get $next) (local.get $frac) {{$fadeArgs}}(i32.const 1) (local.get $g))
            )
            (f32.const 0.5)
        ))
{{- if .BufreadMonoPlain}}
    )(else
{{- end}}
{{- end}}
{{- if .BufreadMonoPlain}}
        (call $push (call $bufreadRead (local.get $h) (local.get $base) (local.get $pos) (local.get $next) (local.get $frac) {{$fadeArgs}}(i32.const 0) (local.get $g)))
{{- if .BufreadMonoMix}}
    ))
{{- end}}
{{- end}}
{{- if .Stereo "bufread"}}
    ))
{{- end}}
{{- if .BufreadPitch}}
    (local.set $semitones (f32.add
        (f32.mul (call $inputSigned (i32.const {{.InputNumber "bufread" "transpose"}})) (f32.const 64))
        (call $inputSigned (i32.const {{.InputNumber "bufread" "detune"}}))
    ))
{{- end}}
{{- if .BufreadNoteTracking}}
    (if (i32.and (i32.load offset=24 (local.get $r)) (i32.const 1)) (then ;; note tracking
        (local.set $semitones (f32.add
            (local.get $semitones)
            (f32.sub (f32.convert_i32_u (i32.load (global.get $voice))) (f32.const 60))
        ))
    ))
{{- end}}
{{- if or .BufreadPitch .BufreadNoteTracking}}
    (local.set $frac (f32.add (local.get $frac) (f32.mul
        (call $inputSigned (i32.const {{.InputNumber "bufread" "speed"}}))
        (call $pow2 (f32.div (local.get $semitones) (f32.const 12)))
    )))
{{- else}}
    ;; no transpose, detune or note tracking in the song: the rate is the speed
    (local.set $frac (f32.add (local.get $frac) (call $inputSigned (i32.const {{.InputNumber "bufread" "speed"}}))))
{{- end}}
    (local.set $whole (f32.floor (local.get $frac)))
    (i32.store (global.get $WRK) (i32.add (local.get $pos) (i32.trunc_f32_s (local.get $whole))))
    (f32.store offset=4 (global.get $WRK) (f32.sub (local.get $frac) (local.get $whole)))
    (i32.store offset=8 (global.get $WRK) (local.get $base))
    (i32.store offset=12 (global.get $WRK) (i32.const 1))
{{- if .BufreadBackwards}}
    (i32.store offset=16 (global.get $WRK) (local.get $inloop))
{{- end}}
)

{{- if .BufreadMod}}
;; $bufreadClearPorts clears the modulations of start, loop start and loop
;; length, which bufread reads itself; those that the song modulates
(func $bufreadClearPorts
{{- if .BufreadModStart}}
    (f32.store offset={{add 32 (mul 4 (.InputNumber "bufread" "start"))}} (global.get $WRK) (f32.const 0))
{{- end}}
{{- if .BufreadModLoopStart}}
    (f32.store offset={{add 32 (mul 4 (.InputNumber "bufread" "loopstart"))}} (global.get $WRK) (f32.const 0))
{{- end}}
{{- if .BufreadModLoopLen}}
    (f32.store offset={{add 32 (mul 4 (.InputNumber "bufread" "looplength"))}} (global.get $WRK) (f32.const 0))
{{- end}}
)
{{- end}}

;; $bufreadOldest returns the oldest valid frame of the buffer with header h
(func $bufreadOldest (param $h i32) (result i32)
    (i32.rem_u
        (i32.sub (i32.add (i32.load offset=12 (local.get $h)) (i32.load offset=4 (local.get $h))) (i32.load offset=16 (local.get $h)))
        (i32.load offset=4 (local.get $h))
    )
)

{{- if .BufreadMod}}
;; $bufreadFrames returns frames shifted by the modulation times the filled
;; length of the buffer, clamped between 0 and the capacity
(func $bufreadFrames (param $frames i32) (param $mod f32) (param $filled i32) (param $cap i32) (result i32) (local $v i64)
    (local.set $v (i64.add
        (i64.extend_i32_s (local.get $frames))
        (i64.trunc_f32_s (f32.max (f32.min
            (f32.floor (f32.mul (local.get $mod) (f32.convert_i32_u (local.get $filled))))
            (f32.const 1073741824)) (f32.const -1073741824)))
    ))
    (i32.wrap_i64 (select (i64.const 0)
        (select (i64.extend_i32_u (local.get $cap)) (local.get $v) (i64.gt_s (local.get $v) (i64.extend_i32_u (local.get $cap))))
        (i64.lt_s (local.get $v) (i64.const 0))
    ))
)
{{- else}}
;; $bufreadClamp returns frames clamped between 0 and the capacity: no
;; position is modulated in the song
(func $bufreadClamp (param $frames i32) (param $cap i32) (result i32)
    (select (i32.const 0)
        (select (local.get $cap) (local.get $frames) (i32.gt_s (local.get $frames) (local.get $cap)))
        (i32.lt_s (local.get $frames) (i32.const 0))
    )
)
{{- end}}

{{- if or .BufreadFade .BufreadEdgeFade}}

(func $minu (param $a i32) (param $b i32) (result i32)
    (select (local.get $a) (local.get $b) (i32.lt_u (local.get $a) (local.get $b)))
)
{{- end}}

;; $bufreadRead returns channel c of the buffer interpolated between frames
;; i and next, crossfaded to the frames before the loop start at the end of
;; the loop, times the gain g
(func $bufreadRead (param $h i32) (param $base i32) (param $i i32) (param $next i32) (param $frac f32){{if .BufreadFade}} (param $ll i32) (param $le i32) (param $fade i32){{end}} (param $c i32) (param $g f32) (result f32) (local $v f32) (local $j i32)
    (local.set $v (call $bufreadLerp (local.get $h) (local.get $base) (local.get $i) (local.get $next) (local.get $frac) (local.get $c)))
{{- if .BufreadFade}}
    (if (i32.and (i32.and (i32.ne (local.get $ll) (i32.const 0)) (i32.ne (local.get $fade) (i32.const 0)))
            (i32.ge_s (local.get $i) (i32.sub (local.get $le) (local.get $fade)))) (then
        (local.set $j (i32.sub (local.get $i) (local.get $ll)))
        (local.set $v (f32.add (local.get $v) (f32.mul
            (f32.sub (call $bufreadLerp (local.get $h) (local.get $base) (local.get $j) (i32.add (local.get $j) (i32.const 1)) (local.get $frac) (local.get $c)) (local.get $v))
            (f32.div
                (f32.add (f32.convert_i32_u (i32.sub (local.get $i) (i32.sub (local.get $le) (local.get $fade)))) (local.get $frac))
                (f32.convert_i32_u (local.get $fade)))
        )))
    ))
{{- end}}
    (f32.mul (local.get $v) (local.get $g))
)

;; $bufreadLerp returns channel c of the buffer interpolated between frames i
;; and next
(func $bufreadLerp (param $h i32) (param $base i32) (param $i i32) (param $next i32) (param $frac f32) (param $c i32) (result f32) (local $a f32)
    (local.set $a (call $bufreadSample (local.get $h) (local.get $base) (local.get $i) (local.get $c)))
    (f32.add
        (local.get $a)
        (f32.mul
            (f32.sub (call $bufreadSample (local.get $h) (local.get $base) (local.get $next) (local.get $c)) (local.get $a))
            (local.get $frac)
        )
    )
)

;; $bufreadSample returns channel c of frame i of a buffer, counted from base,
;; or 0 outside the valid frames
(func $bufreadSample (param $h i32) (param $base i32) (param $i i32) (param $c i32) (result f32) (local $cap i32) (local $abs i32) (local $channels i32)
    (local.set $cap (i32.load offset=4 (local.get $h)))
    (if (i32.ge_u (local.get $i) (local.get $cap)) (then (return (f32.const 0))))
    (local.set $abs (i32.rem_u (i32.add (local.get $base) (local.get $i)) (local.get $cap)))
    (if (i32.ge_u
            (i32.rem_u (i32.sub (i32.add (local.get $abs) (local.get $cap)) (call $bufreadOldest (local.get $h))) (local.get $cap))
            (i32.load offset=16 (local.get $h))) (then
        (return (f32.const 0))
    ))
    (local.set $channels (i32.load offset=8 (local.get $h)))
{{- if .BufreadChannelClamp}}
    (local.set $c (select (local.get $c) (i32.sub (local.get $channels) (i32.const 1)) (i32.lt_u (local.get $c) (local.get $channels))))
{{- end}}
    (f32.load offset={{index .Labels "su_buffers"}} (i32.add
        (i32.load (local.get $h))
        (i32.shl (i32.add (i32.mul (local.get $abs) (local.get $channels)) (local.get $c)) (i32.const 2))
    ))
)
{{end}}
