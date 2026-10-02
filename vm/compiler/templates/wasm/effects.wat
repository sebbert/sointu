{{- if .HasOp "distort"}}
;;-------------------------------------------------------------------------------
;;   DISTORT opcode: apply distortion on the signal
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  x*a/(1-a+(2*a-1)*abs(x))            where x is clamped first
;;   Stereo: l r ->  l*a/(1-a+(2*a-1)*abs(l)) r*a/(1-a+(2*a-1)*abs(r))
;;-------------------------------------------------------------------------------
(func $su_op_distort (param $stereo i32)
{{- if .Stereo "distort"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "distort") 2}}))
{{- end}}
    (call $pop)
    (call $waveshaper (call $input (i32.const {{.InputNumber "distort" "drive"}})))
    (call $push)
)
{{end}}


{{- if .HasOp "hold"}}
;;-------------------------------------------------------------------------------
;;   HOLD opcode: sample and hold the signal, reducing sample rate
;;-------------------------------------------------------------------------------
;;   Mono version:   holds the signal at a rate defined by the freq parameter
;;   Stereo version: holds both channels
;;-------------------------------------------------------------------------------
(func $su_op_hold (param $stereo i32) (local $phase f32)
{{- if .Stereo "hold"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "hold") 2}}))
{{- end}}
    (local.set $phase
        (f32.sub
            (f32.load (global.get $WRK))
            (f32.mul
                (call $input (i32.const {{.InputNumber "hold" "holdfreq"}}))
                (call $input (i32.const {{.InputNumber "hold" "holdfreq"}})) ;; if we ever implement $dup, replace with that
            )
        )
    )
    (if (f32.ge (f32.const 0) (local.get $phase)) (then
        (f32.store offset=4 (global.get $WRK) (call $peek)) ;; we start holding a new value
        (local.set $phase (f32.add (local.get $phase) (f32.const 1)))
    ))
    (drop (call $pop))                                 ;; we replace the top most signal
    (call $push (f32.load offset=4 (global.get $WRK))) ;; with the held value
    (f32.store (global.get $WRK) (local.get $phase)) ;; save back new phase
)
{{end}}


{{- if .HasOp "crush"}}
;;-------------------------------------------------------------------------------
;;   CRUSH opcode: quantize the signal to finite number of levels
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  e*int(x/e)
;;   Stereo: l r ->  e*int(l/e) e*int(r/e)
;;-------------------------------------------------------------------------------
(func $su_op_crush (param $stereo i32) (local $e f32)
{{- if .Stereo "crush"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "crush") 2}}))
{{- end}}
    call $pop
    (f32.div (local.tee $e (call $nonLinearMap (i32.const {{.InputNumber "crush" "resolution"}}))))
    f32.nearest
    (f32.mul (local.get $e))
    call $push
)
{{end}}


{{- if .HasOp "gain"}}
;;-------------------------------------------------------------------------------
;;   GAIN opcode: apply gain on the signal
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  x*g
;;   Stereo: l r ->  l*g r*g
;;-------------------------------------------------------------------------------
(func $su_op_gain (param $stereo i32)
{{- if .Stereo "gain"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "gain") 2}}))
{{- end}}
    (call $push (f32.mul (call $pop) (call $input (i32.const {{.InputNumber "gain" "gain"}}))))
)
{{end}}


{{- if .HasOp "invgain"}}
;;-------------------------------------------------------------------------------
;;   INVGAIN opcode: apply inverse gain on the signal
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  x/g
;;   Stereo: l r ->  l/g r/g
;;-------------------------------------------------------------------------------
(func $su_op_invgain (param $stereo i32)
{{- if .Stereo "invgain"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "invgain") 2}}))
{{- end}}
    (call $push (f32.div (call $pop) (call $input (i32.const {{.InputNumber "invgain" "invgain"}}))))
)
{{end}}

{{- if .HasOp "dbgain"}}
;;-------------------------------------------------------------------------------
;;   DBGAIN opcode: apply gain on the signal, with gain given in decibels
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  x*g, where g = 2**((2*d-1)*6.643856189774724) i.e. -40dB to 40dB, d=[0..1]
;;   Stereo: l r ->  l*g r*g
;;-------------------------------------------------------------------------------
(func $su_op_dbgain (param $stereo i32)
{{- if .Stereo "dbgain"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "dbgain") 2}}))
{{- end}}
    (call $input (i32.const {{.InputNumber "dbgain" "decibels"}}))
    (f32.sub (f32.const 0.5))
    (f32.mul (f32.const 13.287712379549449))
    (call $pow2)
    (f32.mul (call $pop))
    (call $push)
)
{{end}}


{{- if .HasOp "filter"}}
;;-------------------------------------------------------------------------------
;;   FILTER opcode: perform low/high/band-pass/notch etc. filtering on the signal
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  filtered(x)
;;   Stereo: l r ->  filtered(l) filtered(r)
;;-------------------------------------------------------------------------------
(func $su_op_filter (param $stereo i32) (local $flags i32) (local $freq f32) (local $high f32) (local $low f32) (local $band f32) (local $retval f32)
{{- if .Stereo "filter"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "filter") 2}}))
    (if (local.get $stereo)(then
        ;; This is hacky: rewind the $VAL one byte backwards as the right channel already
        ;; scanned it once. Find a way to avoid rewind
        (global.set $VAL (i32.sub (global.get $VAL) (i32.const 1)))
    ))
{{- end}}
    (local.set $flags (call $scanOperand))
    (local.set $freq (f32.mul
        (call $input (i32.const {{.InputNumber "filter" "frequency"}}))
        (call $input (i32.const {{.InputNumber "filter" "frequency"}}))
    ))
    (local.set $low ;; l' = f2*b + l
        (f32.add ;; f2*b+l
            (f32.mul ;; f2*b
                (local.tee $band (f32.load offset=4 (global.get $WRK))) ;; b
                (local.get $freq)                     ;; f2
            )
            (f32.load (global.get $WRK)) ;; l
        )
    )
    (local.set $high ;; h' = x - l' - r*b
        (f32.sub ;; x - l' - r*b
            (f32.sub ;; x - l'
                (call $pop)      ;; x (signal)
                (local.get $low) ;; l'
            )
            (f32.mul ;; r*b
                (call $input (i32.const {{.InputNumber "filter" "resonance"}})) ;; r
                (local.get $band) ;; b
            )
        )
    )
    (local.set $band ;; b' = f2 * h' + b
        (f32.add ;; f2 * h' +  b
            (f32.mul ;; f2 * h'
                (local.get $freq) ;; f2
                (local.get $high) ;; h'
            )
            (local.get $band) ;; b
        )
    )
    (local.set $retval (f32.const 0))
{{- if .SupportsParamValue "filter" "lowpass" 1}}
    (if (i32.and (local.get $flags) (i32.const 0x40)) (then
        (local.set $retval (f32.add (local.get $retval) (local.get $low)))
    ))
{{- end}}
{{- if .SupportsParamValue "filter" "bandpass" 1}}
    (if (i32.and (local.get $flags) (i32.const 0x20)) (then
        (local.set $retval (f32.add (local.get $retval) (local.get $band)))
    ))
{{- end}}
{{- if .SupportsParamValue "filter" "highpass" 1}}
    (if (i32.and (local.get $flags) (i32.const 0x10)) (then
        (local.set $retval (f32.add (local.get $retval) (local.get $high)))
    ))
{{- end}}
{{- if .SupportsParamValue "filter" "bandpass" -1}}
    (if (i32.and (local.get $flags) (i32.const 0x08)) (then
        (local.set $retval (f32.sub (local.get $retval) (local.get $band)))
    ))
{{- end}}
{{- if .SupportsParamValue "filter" "highpass" -1}}
    (if (i32.and (local.get $flags) (i32.const 0x04)) (then
        (local.set $retval (f32.sub (local.get $retval) (local.get $high)))
    ))
{{- end}}
    (f32.store (global.get $WRK) (local.get $low))
    (f32.store offset=4 (global.get $WRK) (local.get $band))
    (call $push (local.get $retval))
)
{{end}}


{{- if .HasOp "belleq"}}
;;-------------------------------------------------------------------------------
;;   BELLEQ opcode: perform second order bell eq filtering on the signal
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  eq(x)
;;   Stereo: l r ->  eq(l) eq(r)
;;-------------------------------------------------------------------------------
(func $su_op_belleq (param $stereo i32) (local $sinw f32) (local $A f32) (local $u f32) (local $v f32) (local $x f32) (local $y f32) (local $d f32) (local $alpha f32)
{{- if .Stereo "belleq"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "belleq") 2}}))
{{- end}}
    (global.get $WRK)
    (local.tee $x (call $pop))                              ;; x WRK
    (f32.mul
        (call $input (i32.const {{.InputNumber "belleq" "frequency"}}))
        (call $input (i32.const {{.InputNumber "belleq" "frequency"}}))
    )
{{- if .MathImports}}
    (f32.mul (f32.const 2))
    (local.tee $sinw (call $sin))                           ;; sinw x WRK
{{- else}}
    (f32.mul (f32.const 0.31830987))                        ;; 2f²/2π turns
    (local.tee $sinw (call $sinTurns))                      ;; sinw x WRK
{{- end}}
    (call $input (i32.const {{.InputNumber "belleq" "bandwidth"}})) ;; b sinw x WRK
    (f32.mul (f32.const 2))                                 ;; 2*b sinw x WRK
    (local.tee $alpha (f32.mul))                            ;; alpha=sinw*2*b x WRK
    (f32.sub (call $input (i32.const {{.InputNumber "belleq" "gain"}})) (f32.const 0.5)) ;; g-0.5 alpha x WRK
    (f32.mul (f32.const 6.643856189774724))
    (local.tee $A (call $pow2))                             ;; A=2^((g-0.5)*6.643856189774724) alpha x WRK
    (local.tee $u (f32.mul))                                ;; u=A*alpha x WRK
    ;; Computing (y=x+u*x+s1)/(1+v)
    (f32.mul (local.get $x))                                ;; u*x x WRK
    (f32.add)                                               ;; ux+x WRK
    (f32.load (global.get $WRK))                            ;; s1 ux+x WRK
    (f32.add)                                               ;; ux+x+s1 WRK
    ;; Compute v=alpha/A
    (local.tee $v (f32.div (local.get $alpha) (local.get $A))) ;; v ux+x+s1 WRK
    (f32.add (f32.const 1))                                 ;; 1+v ux+x+s1 WRK
    (local.tee $y (f32.div))                                ;; y WRK
    ;; s1' = 2*cos(w)*(y-x)+s2
    (f32.sub (local.get $x))                                ;; y-x WRK
    ;; need to compute cos(w) as sqrt(1-sin(w)^2)
    (f32.sqrt (f32.sub (f32.const 1) (f32.mul (local.get $sinw) (local.get $sinw)))) ;; cos(w) y-x WRK
    (f32.mul)                                               ;; cos(w)*(y-x) WRK
    (f32.mul (f32.const 2))                                 ;; 2*cos(w)*(y-x) WRK
    (f32.add (f32.load offset=4 (global.get $WRK)))         ;; s2+2*cos(w)*(y-x) WRK
    (f32.store)                                             ;; s1'=s2+2*cos(w)*(y-x)
    ;; s2' = x-y+v*y-u*x
    (global.get $WRK)
    (f32.sub (local.get $x) (local.get $y))                 ;; x-y WRK
    (f32.mul (local.get $v) (local.get $y))
    (f32.mul (local.get $u) (local.get $x))
    (f32.sub)                                               ;; v*y-u*x x-y WRK
    (f32.add)                                               ;; v*y-u*x+x-y WRK
    (f32.store offset=4)
    (call $push (local.get $y))
)
{{end}}


{{- if .HasOp "clip"}}
;;-------------------------------------------------------------------------------
;;   CLIP opcode: clips the signal into [-1,1] range
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  min(max(x,-1),1)
;;   Stereo: l r ->  min(max(l,-1),1) min(max(r,-1),1)
;;-------------------------------------------------------------------------------
(func $su_op_clip (param $stereo i32)
{{- if .Stereo "clip"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "clip") 2}}))
{{- end}}
    (call $push (call $clip (call $pop)))
)
{{end}}


{{- if .HasOp "pan" -}}
;;-------------------------------------------------------------------------------
;;   PAN opcode: pan the signal
;;-------------------------------------------------------------------------------
;;   Mono:   s   ->  s*(1-p) s*p
;;   Stereo: l r ->  l*(1-p) r*p
;;
;;   where p is the panning in [0,1] range
;;-------------------------------------------------------------------------------
(func $su_op_pan (param $stereo i32)
{{- if .Stereo "pan"}}
    (if (i32.eqz (local.get $stereo)) (then ;; this time, if this is mono op...
        call $peek                         ;;    ...we duplicate the mono into stereo first
        call $push
    ))
    (call $pop)  ;; F: r        P: l
    (call $pop)  ;; F:          P: r l
    (call $input (i32.const {{.InputNumber "pan" "panning"}})) ;; F:        P: p r l
    f32.mul      ;; F:          P: p*r l
    (call $push) ;; F: p*r      P: l
    f32.const 1
    (call $input (i32.const {{.InputNumber "pan" "panning"}})) ;; F: p*r       P: p 1 l
    f32.sub      ;; F: p*r      P: 1-p l
    f32.mul      ;; F: p*r      P: (1-p)*l
    (call $push) ;; F: (1-p)*l p*r
{{- else}}
    (call $peek) ;; F: s       P: s
    (f32.mul
        (call $input (i32.const {{.InputNumber "pan" "panning"}}))
        (call $pop)
    )            ;; F:         P: p*s s
    (call $push) ;; F: p*s     P: s
    (call $peek) ;; F: p*s     P: p*s s
    f32.sub      ;; F: p*s     P: s-p*s
    (call $push) ;; F: (1-p)*s p*s
{{- end}}
)
{{end}}


{{- if .HasOp "delay"}}
;;-------------------------------------------------------------------------------
;;   DELAY opcode: adds delay effect to the signal
;;-------------------------------------------------------------------------------
;;   Mono:   perform delay on ST0, using delaycount delaylines starting
;;           at delayindex from the delaytable
;;   Stereo: perform delay on ST1, using delaycount delaylines starting
;;           at delayindex + delaycount from the delaytable (so the right delays
;;           can be different)
;;-------------------------------------------------------------------------------
(func $su_op_delay (param $stereo i32) (local $delayIndex i32) (local $delayCount i32) (local $output f32) (local $s f32) (local $filtstate f32)
{{- if .Stereo "delay"}} (local $delayCountStash i32) {{- end}}
{{- if or (.SupportsModulation "delay" "delaytime") (.SupportsParamValue "delay" "notetracking" 1)}} (local $delayTime f32) {{- end}}
    (local.set $delayIndex (i32.mul (call $scanOperand) (i32.const 2)))
{{- if .Stereo "delay"}}
    (local.set $delayCountStash (call $scanOperand))
    (if (local.get $stereo)(then
        (call $su_op_xch (i32.const 0))
    ))
    loop $stereoLoop
    (local.set $delayCount (local.get $delayCountStash))
{{- else}}
    (local.set $delayCount (call $scanOperand))
{{- end}}
    (local.set $output (f32.mul
        (call $input (i32.const {{.InputNumber "delay" "dry"}}))
        (call $peek)
    ))
    loop $delayLoop
        (local.tee $s (f32.load offset=12
            (i32.add ;; delayWRK + ((globalTick-delaytimes[delayIndex])&65535)*4
                (i32.mul ;; ((globalTick-delaytimes[delayIndex])&65535)*4
                    (i32.and ;; (globalTick-delaytimes[delayIndex])&65535
                        (i32.sub ;; globalTick-delaytimes[delayIndex]
                            (global.get $globaltick)
{{- if or (.SupportsModulation "delay" "delaytime") (.SupportsParamValue "delay" "notetracking" 1)}} ;; delaytime modulation or note syncing require computing the delay time in floats
{{- if .SupportsModulation "delay" "delaytime"}}
                            (local.set $delayTime (f32.add
                                (f32.convert_i32_u (i32.load16_u
                                    offset={{index .Labels "su_delay_times"}}
                                    (local.get $delayIndex)
                                ))
                                (f32.mul
                                    (f32.load offset={{.InputNumber "delay" "delaytime" | mul 4 | add 32}} (global.get $WRK))
                                    (f32.const 32767)
                                )
                            ))
{{- else}}
                            (local.set $delayTime (f32.convert_i32_u (i32.load16_u
                                    offset={{index .Labels "su_delay_times"}}
                                    (local.get $delayIndex)
                            )))
{{- end}}
{{- if .SupportsParamValue "delay" "notetracking" 1}}
                            (if (i32.eqz (i32.and (local.get $delayCount) (i32.const 1)))(then
                                (local.set $delayTime (f32.div
                                    (local.get $delayTime)
                                    (call $pow2
                                        (f32.mul
                                            (f32.convert_i32_u (i32.load (global.get $voice)))
                                            (f32.const 0.08333333)
                                        )
                                    )
                                ))
                            ))
{{- end}}
                            (i32.trunc_f32_s (f32.add (local.get $delayTime) (f32.const 0.5)))
{{- else}}
                            (i32.load16_u
                                offset={{index .Labels "su_delay_times"}}
                                (local.get $delayIndex)
                            )
{{- end}}
                        )
                        (i32.const 65535)
                    )
                    (i32.const 4)
                )
                (global.get $delayWRK)
            )
        ))
        (local.set $output (f32.add (local.get $output)))
        (f32.store
            (global.get $delayWRK)
            (local.tee $filtstate
                (f32.add
                    (f32.mul
                        (f32.sub
                            (f32.load (global.get $delayWRK))
                            (local.get $s)
                        )
                        (call $input (i32.const {{.InputNumber "delay" "damp"}}))
                    )
                    (local.get $s)
                )
            )
        )
        (f32.store offset=12
            (i32.add ;; delayWRK + globalTick*4
                (i32.mul ;; globalTick)&65535)*4
                    (i32.and ;; globalTick&65535
                        (global.get $globaltick)
                        (i32.const 65535)
                    )
                    (i32.const 4)
                )
                (global.get $delayWRK)
            )
            (f32.add
                (f32.mul
                    (call $input (i32.const {{.InputNumber "delay" "feedback"}}))
                    (local.get $filtstate)
                )
                (f32.mul
                    (f32.mul
                        (call $input (i32.const {{.InputNumber "delay" "pregain"}}))
                        (call $input (i32.const {{.InputNumber "delay" "pregain"}}))
                    )
                    (call $peek)
                )
            )
        )
        (global.set $delayWRK (i32.add (global.get $delayWRK) (i32.const 262156)))
        (local.set $delayIndex (i32.add (local.get $delayIndex) (i32.const 2)))
        (br_if $delayLoop (i32.gt_s (local.tee $delayCount (i32.sub (local.get $delayCount) (i32.const 2))) (i32.const 0)))
    end
    (f32.store offset=4
        (global.get $delayWRK)
        (local.tee $filtstate
            (f32.add
                (local.get $output)
                (f32.sub
                    (f32.mul
                        (f32.const 0.99609375)
                        (f32.load offset=4 (global.get $delayWRK))
                    )
                    (f32.load offset=8 (global.get $delayWRK))
                )
            )
        )
    )
    (f32.store offset=8
        (global.get $delayWRK)
        (local.get $output)
    )
    (drop (call $pop))
    (call $push (local.get $filtstate))
{{- if .Stereo "delay"}}
    (call $su_op_xch (i32.const 0))
    (br_if $stereoLoop (i32.eqz (local.tee $stereo (i32.eqz (local.get $stereo)))))
    end
    (call $su_op_xch (i32.const 0))
{{- end}}
{{- if .SupportsModulation "delay" "delaytime"}}
    (f32.store offset={{.InputNumber "delay" "delaytime" | mul 4 | add 32}} (global.get $WRK) (f32.const 0))
{{- end}}
)
{{end}}


{{- if .HasOp "compressor"}}
;;-------------------------------------------------------------------------------
;;   COMPRES opcode: push compressor gain to stack
;;-------------------------------------------------------------------------------
;;   Mono:   push g on stack, where g is a suitable gain for the signal
;;           you can either MULP to compress the signal or SEND it to a GAIN
;;           somewhere else for compressor side-chaining.
;;   Stereo: push g g on stack, where g is calculated using l^2 + r^2
;;-------------------------------------------------------------------------------
(func $su_op_compressor (param $stereo i32) (local $x2 f32) (local $level f32) (local $t2 f32)
{{- if .Stereo "compressor"}}
    (local.set $x2 (f32.mul
        (call $peek)
        (call $peek)
    ))
    (if (local.get $stereo)(then
        (call $pop)
        (local.set $x2 (f32.add
            (local.get $x2)
            (f32.mul
                (call $peek)
                (call $peek)
            )
        ))
        (call $push)
    ))
    (local.get $x2)
{{- else}}
    (local.tee $x2 (f32.mul
        (call $peek)
        (call $peek)
    ))
{{- end}}
    (local.tee $level (f32.load (global.get $WRK)))
    f32.lt
    call $nonLinearMap ;; $nonlinearMap(x^2<level) (let's call it c)
    (local.tee $level (f32.add ;; l'=l + c*(x^2-l)
        (f32.mul ;; c was already on stack, so c*(x^2-l)
            (f32.sub ;; x^2-l
                (local.get $x2)
                (local.get $level)
            )
        )
        (local.get $level)
    ))
    (local.tee $t2 (f32.mul ;; t^2
        (call $input (i32.const {{.InputNumber "compressor" "threshold"}}))
        (call $input (i32.const {{.InputNumber "compressor" "threshold"}}))
    ))
    (if (f32.gt) (then ;; if $level > $threshold, note the local.tees
        (call $push
            (call {{if .MathImports}}$pow{{else}}$powf{{end}} ;; (t^2/l)^(r/2)
                (f32.div ;; t^2/l
                    (local.get $t2)
                    (local.get $level)
                )
                (f32.mul ;; r/2
                    (call $input (i32.const {{.InputNumber "compressor" "ratio"}})) ;; r
                    (f32.const 0.5)  ;; 0.5
                )
            )
        )
    )(else
        (call $push (f32.const 1)) ;; unity gain if we are below threshold
    ))
    (call $push (f32.div ;; apply post-gain ("make up gain")
        (call $pop)
        (call $input (i32.const {{.InputNumber "compressor" "invgain"}}))
    ))
{{- if .Stereo "compressor"}}
    (if (local.get $stereo)(then
        (call $push (call $peek))
    ))
{{- end}}
    (f32.store (global.get $WRK) (local.get $level)) ;; save the updated levels
)
{{- end}}

{{- if .HasOp "ott"}}
;;-------------------------------------------------------------------------------
;;   OTT opcode: three-band upward and downward compressor
;;-------------------------------------------------------------------------------
;;   Mono:   compresses ST0
;;   Stereo: compresses ST0 and ST1, with one level for each band, from the sum
;;           of the powers of the channels
;;   The state of the unit, 11 floats, is at $ottWRK, in su_ott: the low and
;;   band of the two crossovers for each channel, then the levels of the
;;   bands. Matches ott in vm/ott.go, where the constants are explained.
;;-------------------------------------------------------------------------------
(func $su_op_ott (param $stereo i32) (local $l0 f32) (local $m0 f32) (local $h0 f32) (local $l1 f32) (local $m1 f32) (local $h1 f32) (local $inv f32) (local $g0 f32) (local $g1 f32)
    (call $ottSplit (call $peek) (global.get $ottWRK))
    (local.set $h0) (local.set $m0) (local.set $l0)
{{- if .Stereo "ott"}}
    (if (local.get $stereo) (then
        (call $ottSplit (call $peek2) (i32.add (global.get $ottWRK) (i32.const 16)))
        (local.set $h1) (local.set $m1) (local.set $l1)
    ))
{{- end}}
{{- if .OttTime}}
    ;; 1 / the time multiplier
    (local.set $inv (call $exp2f (f32.mul
        (f32.sub (f32.const 0.5) (call $input (i32.const {{.InputNumber "ott" "time"}})))
        (f32.const 8)
    )))
{{- end}}
    ;; in mono, the right channel's bands are 0 and add nothing to the powers:
    ;; without a stereo ott in the song, they are left out
    (local.set $g0 (call $ottGain
        {{if .Stereo "ott"}}(f32.add (f32.mul (local.get $l0) (local.get $l0)) (f32.mul (local.get $l1) (local.get $l1))){{else}}(f32.mul (local.get $l0) (local.get $l0)){{end}}
        (i32.add (global.get $ottWRK) (i32.const 32)){{if .OttTime}} (local.get $inv){{end}}
        (f32.const -0.00068439695) (f32.const -0.00011600771) (f32.const -11.228117) (f32.const -13.553467)
        (call $input (i32.const {{.InputNumber "ott" "low"}}))
    ))
    (local.set $g1 (call $ottGain
        {{if .Stereo "ott"}}(f32.add (f32.mul (local.get $m0) (local.get $m0)) (f32.mul (local.get $m1) (local.get $m1))){{else}}(f32.mul (local.get $m0) (local.get $m0)){{end}}
        (i32.add (global.get $ottWRK) (i32.const 36)){{if .OttTime}} (local.get $inv){{end}}
        (f32.const -0.0014604542) (f32.const -0.00011600771) (f32.const -10.032223) (f32.const -13.885659)
        (call $input (i32.const {{.InputNumber "ott" "mid"}}))
    ))
    (local.set $inv (call $ottGain
        {{if .Stereo "ott"}}(f32.add (f32.mul (local.get $h0) (local.get $h0)) (f32.mul (local.get $h1) (local.get $h1))){{else}}(f32.mul (local.get $h0) (local.get $h0)){{end}}
        (i32.add (global.get $ottWRK) (i32.const 40)){{if .OttTime}} (local.get $inv){{end}}
        (f32.const -0.0024232720) (f32.const -0.00024783466) (f32.const -11.792845) (f32.const -13.553467)
        (call $input (i32.const {{.InputNumber "ott" "high"}}))
    )) ;; $inv is the gain of the high band from here on
    (f32.store (global.get $sp) (call $ottMix (call $peek) (local.get $l0) (local.get $m0) (local.get $h0) (local.get $g0) (local.get $g1) (local.get $inv)))
{{- if .Stereo "ott"}}
    (if (local.get $stereo) (then
        (f32.store offset=4 (global.get $sp) (call $ottMix (call $peek2) (local.get $l1) (local.get $m1) (local.get $h1) (local.get $g0) (local.get $g1) (local.get $inv)))
    ))
{{- end}}
    (global.set $ottWRK (i32.add (global.get $ottWRK) (i32.const 44)))
)

;; $ottSplit splits $x into the low, mid and high bands of ott, with the
;; state of the crossovers of the channel at $s
(func $ottSplit (param $x f32) (param $s i32) (result f32 f32 f32) (local $low f32) (local $rest f32) (local $mid f32)
    (local.set $low (f32.add (f32.load (local.get $s)) (f32.mul (f32.const 0.012580535) (f32.load offset=4 (local.get $s)))))
    (local.set $rest (f32.sub (local.get $x) (local.get $low)))
    (local.set $mid (f32.add (f32.load offset=8 (local.get $s)) (f32.mul (f32.const 0.35430971) (f32.load offset=12 (local.get $s)))))
    (f32.store offset=4 (local.get $s) (f32.add
        (f32.load offset=4 (local.get $s))
        (f32.mul (f32.const 0.012580535) (f32.sub (local.get $rest) (f32.mul (f32.const 1.4142135) (f32.load offset=4 (local.get $s)))))
    ))
    (f32.store offset=12 (local.get $s) (f32.add
        (f32.load offset=12 (local.get $s))
        (f32.mul (f32.const 0.35430971) (f32.sub (f32.sub (local.get $rest) (local.get $mid)) (f32.mul (f32.const 1.4142135) (f32.load offset=12 (local.get $s)))))
    ))
    (f32.store (local.get $s) (local.get $low))
    (f32.store offset=8 (local.get $s) (local.get $mid))
    (local.get $low)
    (local.get $mid)
    (f32.sub (local.get $rest) (local.get $mid))
)

;; $ottGain moves the level of a band at $a toward the power $x2 of the band
;; and returns the gain of the band: downward above $down, upward below $up,
;; times the band's gain parameter $gain. $inv is 1 over the time multiplier,
;; in songs that set the time.
(func $ottGain (param $x2 f32) (param $a i32){{if .OttTime}} (param $inv f32){{end}} (param $attack f32) (param $release f32) (param $down f32) (param $up f32) (param $gain f32) (result f32) (local $level f32) (local $g f32)
    (local.set $level (f32.load (local.get $a)))
    (local.set $level (f32.add (local.get $level) (f32.mul
        (f32.sub (local.get $x2) (local.get $level))
{{- if .OttTime}}
        (f32.sub (f32.const 1) (call $exp2f (f32.mul
            (select (local.get $release) (local.get $attack) (f32.lt (local.get $x2) (local.get $level)))
            (local.get $inv)
        )))
{{- else}}
        (f32.sub (f32.const 1) (call $exp2f
            (select (local.get $release) (local.get $attack) (f32.lt (local.get $x2) (local.get $level)))
        ))
{{- end}}
    )))
    (f32.store (local.get $a) (local.get $level))
    (local.set $level (call $log2f (local.get $level)))
    (local.set $g (f32.mul (f32.sub (local.get $gain) (f32.const 0.5)) (f32.const 8)))
{{- if .OttDownward}}
    (if (f32.gt (local.get $level) (local.get $down)) (then ;; downward
        (local.set $g (f32.sub (local.get $g) (f32.mul
            (f32.mul (f32.sub (local.get $level) (local.get $down)) (f32.const 0.49250376))
            (call $input (i32.const {{.InputNumber "ott" "downward"}}))
        )))
    ))
{{- end}}
{{- if .OttUpward}}
    (if (f32.lt (local.get $level) (local.get $up)) (then ;; upward
        (local.set $g (f32.add (local.get $g) (f32.min
            (f32.mul
                (f32.mul (f32.sub (local.get $up) (local.get $level)) (f32.const 0.375))
                (call $input (i32.const {{.InputNumber "ott" "upward"}}))
            )
            (f32.const 3.9863138)
        )))
    ))
{{- end}}
    (call $exp2f (local.get $g))
)

;; $ottMix sums the bands of a channel with their gains and mixes them with
;; the dry signal $x by depth
(func $ottMix (param $x f32) (param $low f32) (param $mid f32) (param $high f32) (param $gl f32) (param $gm f32) (param $gh f32) (result f32)
    (f32.add (local.get $x) (f32.mul
        (f32.sub
            (f32.add
                (f32.add (f32.mul (local.get $low) (local.get $gl)) (f32.mul (local.get $mid) (local.get $gm)))
                (f32.mul (local.get $high) (local.get $gh))
            )
            (local.get $x)
        )
        (call $input (i32.const {{.InputNumber "ott" "depth"}}))
    ))
)
{{end}}

{{- if .HasOp "limiter"}}
;;-------------------------------------------------------------------------------
;;   LIMITER opcode: lookahead peak limiter
;;-------------------------------------------------------------------------------
;;   Mono:   limits ST0
;;   Stereo: limits ST0 and ST1, with one gain for both
;;   The state of the unit is at $limiterWRK, in su_limiter: the level, the
;;   reduction, the frame of the delay line to write next, and from offset 16
;;   the delay line, 512 frames of two floats. Matches limiter in
;;   vm/limiter.go, where it is explained.
;;-------------------------------------------------------------------------------
(func $su_op_limiter (param $stereo i32) (local $lookahead i32) (local $in i32) (local $out i32) (local $peak f32) (local $level f32) (local $red f32)
{{- if .LimiterDrive}} (local $drive f32){{end}}
    (local.set $lookahead (i32.shl (call $scanOperand) (i32.const 2))) ;; in steps of 4 samples
    (local.set $in (i32.add
        (global.get $limiterWRK)
        (i32.shl (i32.load offset=8 (global.get $limiterWRK)) (i32.const 3))
    ))
    (local.set $out (i32.add
        (global.get $limiterWRK)
        (i32.shl (i32.and (i32.sub (i32.load offset=8 (global.get $limiterWRK)) (local.get $lookahead)) (i32.const 511)) (i32.const 3))
    ))
{{- if .LimiterDrive}}
    (local.set $drive (f32.add (f32.const 1) (f32.mul (f32.const 7) (call $input (i32.const {{.InputNumber "limiter" "drive"}})))))
    (f32.store offset=16 (local.get $in) (f32.mul (call $peek) (local.get $drive)))
{{- else}}
    (f32.store offset=16 (local.get $in) (call $peek))
{{- end}}
    ;; the peak of what goes into the delay line and of what comes out of it
    (local.set $peak (f32.max (f32.abs (f32.load offset=16 (local.get $in))) (f32.abs (f32.load offset=16 (local.get $out)))))
{{- if .Stereo "limiter"}}
    (if (local.get $stereo) (then
{{- if .LimiterDrive}}
        (f32.store offset=20 (local.get $in) (f32.mul (call $peek2) (local.get $drive)))
{{- else}}
        (f32.store offset=20 (local.get $in) (call $peek2))
{{- end}}
        (local.set $peak (f32.max (local.get $peak)
            (f32.max (f32.abs (f32.load offset=20 (local.get $in))) (f32.abs (f32.load offset=20 (local.get $out))))
        ))
    ))
{{- end}}
    ;; the level jumps to a higher peak and falls back by release
    (local.set $level (f32.load (global.get $limiterWRK)))
    (local.set $level (f32.max
        (f32.sub (local.get $level) (f32.mul (local.get $level) (call $nonLinearMap (i32.const {{.InputNumber "limiter" "release"}}))))
        (local.get $peak)
    ))
    (f32.store (global.get $limiterWRK) (local.get $level))
    ;; the reduction that brings the level down to the threshold ($peak is the target from here on)
    (local.set $peak (f32.const 0))
    (if (f32.gt (local.get $level) (call $input (i32.const {{.InputNumber "limiter" "threshold"}}))) (then
        (local.set $peak (f32.sub (f32.const 1) (f32.div (call $input (i32.const {{.InputNumber "limiter" "threshold"}})) (local.get $level))))
    ))
    (local.set $red (f32.load offset=4 (global.get $limiterWRK)))
    (local.set $red (f32.add (local.get $red) (f32.mul
        (f32.sub (local.get $peak) (local.get $red))
        (f32.div (f32.const 4) (f32.convert_i32_u (i32.add (local.get $lookahead) (i32.const 4))))
    )))
    (f32.store offset=4 (global.get $limiterWRK) (local.get $red))
    (local.set $red (f32.sub (f32.const 1) (local.get $red))) ;; the gain
    (f32.store (global.get $sp) (f32.mul (f32.load offset=16 (local.get $out)) (local.get $red)))
{{- if .Stereo "limiter"}}
    (if (local.get $stereo) (then
        (f32.store offset=4 (global.get $sp) (f32.mul (f32.load offset=20 (local.get $out)) (local.get $red)))
    ))
{{- end}}
    (i32.store offset=8 (global.get $limiterWRK) (i32.and (i32.add (i32.load offset=8 (global.get $limiterWRK)) (i32.const 1)) (i32.const 511)))
    (global.set $limiterWRK (i32.add (global.get $limiterWRK) (i32.const 4112)))
)
{{end}}

{{- if .HasOp "softclip"}}
;;-------------------------------------------------------------------------------
;;   SOFTCLIP opcode: clipper with a soft knee
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  softclip(x*drive)
;;   Stereo: l r ->  softclip(l*drive) softclip(r*drive)
;;   With the operand 1, at twice the sample rate: the state of the unit is
;;   then the four allpasses of the half-band filters, for each channel.
;;   Matches softclip and softclipOversampled in vm/shaping.go, where they
;;   are explained.
;;-------------------------------------------------------------------------------
{{- /* without drive in the song, the gain is 1 and left out */}}
{{- $left := "(call $peek)"}}{{$right := "(call $peek2)"}}
{{- if .SoftclipDrive}}
{{- $left = "(f32.mul (call $peek) (local.get $drive))"}}{{$right = "(f32.mul (call $peek2) (local.get $drive))"}}
{{- end}}
(func $su_op_softclip (param $stereo i32) (local $drive f32)
{{- if .SoftclipOversample}} (local $over i32){{end}}
{{- if .SoftclipDrive}}
    (local.set $drive (f32.add (f32.const 1) (f32.mul (f32.const 7) (call $input (i32.const {{.InputNumber "softclip" "drive"}})))))
{{- end}}
{{- if .SoftclipOversample}}
    (local.set $over (call $scanOperand))
    (f32.store (global.get $sp) (call $softclipOver {{$left}} (global.get $WRK) (local.get $over)))
{{- else}}
    (f32.store (global.get $sp) (call $softclip {{$left}}))
{{- end}}
{{- if .Stereo "softclip"}}
    (if (local.get $stereo) (then
{{- if .SoftclipOversample}}
        (f32.store offset=4 (global.get $sp) (call $softclipOver {{$right}} (i32.add (global.get $WRK) (i32.const 16)) (local.get $over)))
{{- else}}
        (f32.store offset=4 (global.get $sp) (call $softclip {{$right}}))
{{- end}}
    ))
{{- end}}
)

;; $softclip leaves $x up to the knee, bends it from there to full scale,
;; reached at 2 - knee, and keeps it there
(func $softclip (param $x f32) (result f32) (local $a f32) (local $knee f32) (local $t f32)
    (local.set $knee (call $input (i32.const {{.InputNumber "softclip" "knee"}})))
    (local.set $a (f32.abs (local.get $x)))
    (if (f32.gt (local.get $a) (local.get $knee)) (then
        (if (f32.ge (local.get $a) (f32.sub (f32.const 2) (local.get $knee))) (then
            (local.set $a (f32.const 1))
        )(else
            (local.set $t (f32.sub (local.get $a) (local.get $knee)))
            (local.set $a (f32.sub (local.get $a) (f32.div
                (f32.mul (local.get $t) (local.get $t))
                (f32.mul (f32.const 4) (f32.sub (f32.const 1) (local.get $knee)))
            )))
        ))
    ))
    (f32.copysign (local.get $a) (local.get $x))
)
{{- if .SoftclipOversample}}

;; $softclipOver is $softclip, at twice the sample rate if $over: the two
;; allpasses of the half-band filter give two samples for $x, and the same
;; two, crossed, the average of the two after the clipper. Their states are
;; at $s.
(func $softclipOver (param $x f32) (param $s i32) (param $over i32) (result f32)
    (if (i32.eqz (local.get $over)) (then
        (return (call $softclip (local.get $x)))
    ))
    (call $allpass (i32.add (local.get $s) (i32.const 12))
        (call $softclip (call $allpass (local.get $s) (local.get $x) (f32.const 0.19104233)))
        (f32.const 0.66083542)
    )
    (local.set $x (call $softclip (call $allpass (i32.add (local.get $s) (i32.const 4)) (local.get $x) (f32.const 0.66083542))))
    (f32.add (call $allpass (i32.add (local.get $s) (i32.const 8)) (local.get $x) (f32.const 0.19104233)))
    (f32.mul (f32.const 0.5))
)

;; $allpass is a first-order allpass with the coefficient $a and the state at $s
(func $allpass (param $s i32) (param $x f32) (param $a f32) (result f32) (local $y f32)
    (local.set $y (f32.add (f32.mul (local.get $a) (local.get $x)) (f32.load (local.get $s))))
    (f32.store (local.get $s) (f32.sub (local.get $x) (f32.mul (local.get $a) (local.get $y))))
    (local.get $y)
)
{{- end}}
{{end}}

{{- if .HasOp "width"}}
;;-------------------------------------------------------------------------------
;;   WIDTH opcode: scale the side signal of a stereo signal
;;-------------------------------------------------------------------------------
;;   Stereo: l r ->  m+s m-s, where m = (l+r)/2 and s = (l-r)/2 * 2*width,
;;           after a high-pass at lowcut, whose low and band are the state.
;;   Matches width in vm/shaping.go.
;;-------------------------------------------------------------------------------
(func $su_op_width (param $stereo i32) (local $mid f32) (local $side f32)
{{- if .WidthLowcut}} (local $freq2 f32) (local $low f32){{end}}
    (local.set $mid (f32.mul (f32.add (call $peek) (call $peek2)) (f32.const 0.5)))
    (local.set $side (f32.mul (f32.sub (call $peek) (call $peek2)) (f32.const 0.5)))
{{- if .WidthLowcut}}
    (local.set $freq2 (f32.mul
        (call $input (i32.const {{.InputNumber "width" "lowcut"}}))
        (call $input (i32.const {{.InputNumber "width" "lowcut"}}))
    ))
    (local.set $low (f32.add (f32.load (global.get $WRK)) (f32.mul (local.get $freq2) (f32.load offset=4 (global.get $WRK)))))
    (local.set $side (f32.sub
        (f32.sub (local.get $side) (local.get $low))
        (f32.mul (f32.const 1.4142135) (f32.load offset=4 (global.get $WRK)))
    ))
    (f32.store offset=4 (global.get $WRK) (f32.add (f32.load offset=4 (global.get $WRK)) (f32.mul (local.get $freq2) (local.get $side))))
    (f32.store (global.get $WRK) (local.get $low))
{{- end}}
    (local.set $side (f32.mul (local.get $side) (f32.add
        (call $input (i32.const {{.InputNumber "width" "width"}}))
        (call $input (i32.const {{.InputNumber "width" "width"}}))
    )))
    (f32.store (global.get $sp) (f32.add (local.get $mid) (local.get $side)))
    (f32.store offset=4 (global.get $sp) (f32.sub (local.get $mid) (local.get $side)))
)
{{end}}

{{- if .HasOp "ladder"}}
;;-------------------------------------------------------------------------------
;;   LADDER opcode: low-pass of 24 dB per octave with resonance and drive
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  filtered(x)
;;   Stereo: l r ->  filtered(l) filtered(r)
;;   The state of the unit is the four low-passes. Matches ladder in
;;   vm/shaping.go, where it is explained.
;;-------------------------------------------------------------------------------
(func $su_op_ladder (param $stereo i32) (local $g f32) (local $g2 f32) (local $k f32) (local $x f32) (local $y f32) (local $i i32)
{{- if .Stereo "ladder"}}
    (call $stereoHelper (local.get $stereo) (i32.const {{div (.GetOp "ladder") 2}}))
{{- end}}
    (local.set $g (f32.min
        (f32.mul
            (call $input (i32.const {{.InputNumber "ladder" "frequency"}}))
            (call $input (i32.const {{.InputNumber "ladder" "frequency"}}))
        )
        (f32.const 0.99)
    ))
    (local.set $k (f32.mul (f32.const 4.5) (call $input (i32.const {{.InputNumber "ladder" "resonance"}}))))
    ;; the gain, and the feedback against half of the input
    (local.set $x (f32.mul
{{- if .LadderDrive}}
        (f32.mul (call $pop) (f32.add (f32.const 1) (f32.mul (f32.const 7) (call $input (i32.const {{.InputNumber "ladder" "drive"}})))))
{{- else}}
        (call $pop)
{{- end}}
        (f32.add (f32.const 1) (f32.mul (f32.const 0.5) (local.get $k)))
    ))
    ;; the output without the saturator: the input through the low-passes, over 1 + k·g⁴
    (local.set $y (local.get $x))
    (loop $estimate
        (local.set $y (f32.add
            (f32.mul (local.get $y) (local.get $g))
            (f32.mul (f32.sub (f32.const 1) (local.get $g)) (f32.load (i32.add (global.get $WRK) (local.get $i))))
        ))
        (br_if $estimate (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 4))) (i32.const 16)))
    )
    (local.set $g2 (f32.mul (local.get $g) (local.get $g)))
    (local.set $x (f32.sub (local.get $x) (f32.mul (local.get $k) (f32.div
        (local.get $y)
        (f32.add (f32.const 1) (f32.mul (local.get $k) (f32.mul (local.get $g2) (local.get $g2))))
    ))))
    ;; the saturator
    (local.set $x (call $clip (f32.mul (local.get $x) (f32.const 0.6666667))))
    (local.set $x (f32.mul
        (f32.sub (local.get $x) (f32.mul (f32.mul (f32.mul (local.get $x) (local.get $x)) (local.get $x)) (f32.const 0.33333334)))
        (f32.const 1.5)
    ))
    ;; the low-passes; $y is the change of each, $i its offset from the last
    (local.set $i (i32.const 0))
    (loop $poles
        (local.set $y (f32.mul (local.get $g) (f32.sub (local.get $x) (f32.load (i32.add (global.get $WRK) (local.get $i))))))
        (local.set $x (f32.add (local.get $y) (f32.load (i32.add (global.get $WRK) (local.get $i)))))
        (f32.store (i32.add (global.get $WRK) (local.get $i)) (f32.add (local.get $x) (local.get $y)))
        (br_if $poles (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 4))) (i32.const 16)))
    )
    (call $push (local.get $x))
)
{{end}}
