{{- if .HasOp "outaux"}}
;;-------------------------------------------------------------------------------
;;   OUTAUX opcode: outputs to main and aux1 outputs and pops the signal
;;-------------------------------------------------------------------------------
;;   Mono: add outgain*ST0 to main left port and auxgain*ST0 to aux1 left
;;   Stereo: also add outgain*ST1 to main right port and auxgain*ST1 to aux1 right
;;-------------------------------------------------------------------------------
(func $su_op_outaux (param $stereo i32) (local $addr i32)
    (local.set $addr (i32.const {{index .Labels "su_globalports"}}))
{{- if .Stereo "outaux"}}
    loop $stereoLoop
{{- end}}
        (f32.store ;; send
            (local.get $addr)
            (f32.add
                (f32.mul
                    (call $peek)
                    (call $input (i32.const {{.InputNumber "outaux" "outgain"}}))
                )
                (f32.load (local.get $addr))
            )
        )
        (f32.store offset=8
            (local.get $addr)
            (f32.add
                (f32.mul
                    (call $pop)
                    (call $input (i32.const {{.InputNumber "outaux" "auxgain"}}))
                )
                (f32.load offset=8 (local.get $addr))
            )
        )
{{- if .Stereo "outaux"}}
        (local.set $addr (i32.add (local.get $addr) (i32.const 4)))
        (br_if $stereoLoop (i32.eqz (local.tee $stereo (i32.eqz (local.get $stereo)))))
    end
{{- end}}
)
{{end}}


{{- if .HasOp "aux"}}
;;-------------------------------------------------------------------------------
;;   AUX opcode: outputs the signal to aux (or main) port and pops the signal
;;-------------------------------------------------------------------------------
;;   Mono: add gain*ST0 to left port
;;   Stereo: also add gain*ST1 to right port
;;-------------------------------------------------------------------------------
(func $su_op_aux (param $stereo i32) (local $addr i32)
    (local.set $addr (i32.add (i32.mul (call $scanOperand) (i32.const 4)) (i32.const {{index .Labels "su_globalports"}})))
{{- if .Stereo "aux"}}
    loop $stereoLoop
{{- end}}
        (f32.store
            (local.get $addr)
            (f32.add
                (f32.mul
                    (call $pop)
                    (call $input (i32.const {{.InputNumber "aux" "gain"}}))
                )
                (f32.load (local.get $addr))
            )
        )
{{- if .Stereo "aux"}}
        (local.set $addr (i32.add (local.get $addr) (i32.const 4)))
        (br_if $stereoLoop (i32.eqz (local.tee $stereo (i32.eqz (local.get $stereo)))))
    end
{{- end}}
)
{{end}}


{{- if .HasOp "send"}}
;;-------------------------------------------------------------------------------
;;   SEND opcode: adds the signal to a port
;;-------------------------------------------------------------------------------
;;   Mono: adds signal to a memory address, defined by a word in VAL stream
;;   Stereo: also add right signal to the following address
;;-------------------------------------------------------------------------------
(func $su_op_send (param $stereo i32) (local $address i32) (local $scaledAddress i32)
    (local.set $address (i32.add (call $scanOperand) (i32.shl (call $scanOperand) (i32.const 8))))
    (if (i32.eqz (i32.and (local.get $address) (i32.const 8)))(then
{{- if .Stereo "send"}}
        (if (local.get $stereo)(then
            (call $push (call $peek2))
            (call $push (call $peek2))
        )(else
{{- end}}
            (call $push (call $peek))
{{- if .Stereo "send"}}
        ))
{{- end}}
    ))
{{- if .Stereo "send"}}
    loop $stereoLoop
{{- end}}
    (local.set $scaledAddress (i32.add (i32.mul (i32.and (local.get $address) (i32.const 0x7FF7)) (i32.const 4))
{{- if .SupportsGlobalSend}}
        (select
            (i32.const {{index .Labels "su_synth"}})
{{- end}}
            (global.get $voice)
{{- if .SupportsGlobalSend}}
            (i32.and (local.get $address)(i32.const 0x8000))
        )
{{- end}}
    ))
    (f32.store offset=32
        (local.get $scaledAddress)
        (f32.add
            (f32.load offset=32 (local.get $scaledAddress))
            (f32.mul
                (call $inputSigned (i32.const {{.InputNumber "send" "amount"}}))
                (call $pop)
            )
        )
    )
    {{- if .Stereo "send"}}
    (local.set $address (i32.add (local.get $address) (i32.const 1)))
    (br_if $stereoLoop (i32.eqz (local.tee $stereo (i32.eqz (local.get $stereo)))))
    end
    {{- end}}
)
{{end}}



{{- if .HasOp "out"}}
;;-------------------------------------------------------------------------------
;;   OUT opcode: outputs and pops the signal
;;-------------------------------------------------------------------------------
{{- if .Mono "out"}}
;;   Mono: add ST0 to main left port, then pop
{{- end}}
{{- if .Stereo "out"}}
;;   Stereo: add ST0 to left out and ST1 to right out, then pop
{{- end}}
;;-------------------------------------------------------------------------------
(func $su_op_out (param $stereo i32) (local $ptr i32)
    (local.set $ptr (i32.const {{index .Labels "su_globalports"}})) ;; synth.left
    (f32.store (local.get $ptr)
        (f32.add
            (f32.mul
                (call $pop)
                (call $input (i32.const {{.InputNumber "out" "gain"}}))
            )
            (f32.load (local.get $ptr))
        )
    )
    {{- if .StereoAndMono "out"}}
    (if (local.get $stereo)(then
    {{- end}}
    {{- if .Stereo "out"}}
    (local.set $ptr (i32.const {{add (index .Labels "su_globalports") 4}})) ;; synth.right
    (f32.store (local.get $ptr)
        (f32.add
            (f32.mul
                (call $pop)
                (call $input (i32.const {{.InputNumber "out" "gain"}}))
            )
            (f32.load (local.get $ptr))
        )
    )
    {{- end}}
    {{- if .StereoAndMono "out"}}
    ))
    {{- end}}
)
{{end}}

{{- if .HasOp "speed"}}
;;-------------------------------------------------------------------------------
;;   SPEED opcode: modulate the speed (bpm) of the song based on ST0
;;-------------------------------------------------------------------------------
;;   Mono: adds or subtracts the ticks, a value of 0.5 is neutral & will7
;;   result in no speed change.
;;   There is no STEREO version.
;;-------------------------------------------------------------------------------
(func $su_op_speed (param $stereo i32) (local $r f32) (local $w i32)
    (f32.store
        (global.get $WRK)
        (local.tee $r
            (f32.sub
                (local.tee $r
                    (f32.add
                        (f32.load (global.get $WRK))
                        (f32.sub
                            (call $pow2
                                (f32.mul
                                    (call $pop)
                                    (f32.const 2.206896551724138)
                                )
                            )
                            (f32.const 1)
                        )
                    )
                )
                (f32.convert_i32_s
                    (local.tee $w (i32.trunc_f32_s (local.get $r))) ;; note: small difference from x86, as this is trunc; x86 rounds to nearest)
                )
            )
        )
    )
    (global.set $sample (i32.add (global.get $sample) (local.get $w)))
)
{{end}}


{{- if .HasOp "spawn"}}
;;-------------------------------------------------------------------------------
;;   SPAWN opcode: triggers notes on the voices of another instrument
;;-------------------------------------------------------------------------------
;;   Pops the input in edge mode, then the arguments. WRK[0] is the time until
;;   the next spawn in rate mode, in periods, and WRK[1] the previous input in
;;   edge mode. At offset 8, voices have the global time + 1 when they were
;;   last spawned, at offset 12 the global time when to release them (0 for
;;   never), at offset 16 the arguments and at offset 32 the length of the
;;   note in frames (0 for none). Matches spawn in
;;   vm/go_synth.go.
;;-------------------------------------------------------------------------------
(func $su_op_spawn (param $stereo i32) (local $first i32) (local $count i32) (local $flags i32) (local $held i32) (local $fire i32) (local $in f32) (local $n f32) (local $target i32) (local $i i32) (local $v i32)
    (local.set $first (call $scanOperand))
    (local.set $count (call $scanOperand))
    (local.set $flags (call $scanOperand))
    ;; release the notes that have lasted their length
    (if (local.get $count) (then
        (local.set $i (local.get $first))
        loop $release_loop
            (local.set $v (i32.add (i32.const {{index .Labels "su_voices"}}) (i32.mul (local.get $i) (i32.const 4096))))
            (if (i32.and
                    (i32.ne (i32.load offset=12 (local.get $v)) (i32.const 0))
                    (i32.ge_u (global.get $globaltick) (i32.load offset=12 (local.get $v)))) (then
                (i32.store offset=4 (local.get $v) (i32.const 0))
                (i32.store offset=12 (local.get $v) (i32.const 0))
            ))
            (br_if $release_loop (i32.lt_u
                (local.tee $i (i32.add (local.get $i) (i32.const 1)))
                (i32.add (local.get $first) (local.get $count))
            ))
        end
    ))
    (local.set $held (i32.and
        (i32.ne (i32.load (global.get $voice)) (i32.const 0))
        (i32.ne (i32.load offset=4 (global.get $voice)) (i32.const 0))
    ))
    (if (i32.and (local.get $flags) (i32.const 1)) (then ;; edge mode
        (local.set $in (call $pop))
        (local.set $fire (i32.and (local.get $held) (i32.and
            (f32.le (f32.load offset=4 (global.get $WRK)) (f32.const 0))
            (f32.gt (local.get $in) (f32.const 0))
        )))
        (f32.store offset=4 (global.get $WRK) (local.get $in))
    )(else
        (if (local.get $held) (then
            (if (f32.le (f32.load (global.get $WRK)) (f32.const 0)) (then
                (local.set $fire (i32.const 1))
                (f32.store (global.get $WRK) (f32.add (f32.load (global.get $WRK)) (f32.const 1)))
            ))
            (f32.store (global.get $WRK) (f32.sub
                (f32.load (global.get $WRK))
                (f32.div
                    (call $pow2 (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "spawn" "rate"}})) (f32.const 16)) (f32.const 5)))
                    (f32.const 44100)
                )
            ))
        ))
    ))
    (local.set $i (i32.shr_u (local.get $flags) (i32.const 2))) ;; number of arguments
    (if (i32.and (local.get $fire) (i32.ne (local.get $count) (i32.const 0))) (then
        ;; take the voice spawned longest ago
        (local.set $target (local.get $first))
        (local.set $v (local.get $first))
        loop $voice_loop
            (if (i32.lt_u
                    (i32.load offset={{add (index .Labels "su_voices") 8}} (i32.mul (local.get $v) (i32.const 4096)))
                    (i32.load offset={{add (index .Labels "su_voices") 8}} (i32.mul (local.get $target) (i32.const 4096)))) (then
                (local.set $target (local.get $v))
            ))
            (br_if $voice_loop (i32.lt_u
                (local.tee $v (i32.add (local.get $v) (i32.const 1)))
                (i32.add (local.get $first) (local.get $count))
            ))
        end
        (local.set $n (select
            (f32.convert_i32_u (i32.load (global.get $voice)))
            (f32.const 60)
            (i32.and (local.get $flags) (i32.const 2)) ;; note tracking
        ))
        (local.set $n (f32.add
            (f32.add
                (local.get $n)
                (f32.mul (call $inputSigned (i32.const {{.InputNumber "spawn" "transpose"}})) (f32.const 64))
            )
            (f32.const 0.5)
        ))
        (local.set $v (i32.add (i32.const {{index .Labels "su_voices"}}) (i32.mul (local.get $target) (i32.const 4096))))
        (memory.fill (local.get $v) (i32.const 0) (i32.const 4096))
        (i32.store (local.get $v) (i32.trunc_f32_s (f32.floor (f32.max (f32.min (local.get $n) (f32.const 127)) (f32.const 1)))))
        (i32.store offset=4 (local.get $v) (i32.load (local.get $v)))
        (i32.store offset=8 (local.get $v) (i32.add (global.get $globaltick) (i32.const 1)))
        (if (f32.gt (call $input (i32.const {{.InputNumber "spawn" "length"}})) (f32.const 0)) (then
            (i32.store offset=32 (local.get $v)
                (i32.trunc_f32_u (f32.max (call $lengthFrames (call $input (i32.const {{.InputNumber "spawn" "length"}}))) (f32.const 1)))
            )
            (i32.store offset=12 (local.get $v) (i32.add (global.get $globaltick) (i32.load offset=32 (local.get $v))))
        ))
        loop $args_loop
            (if (local.get $i) (then
                (local.set $i (i32.sub (local.get $i) (i32.const 1)))
                (f32.store offset=16 (i32.add (local.get $v) (i32.shl (local.get $i) (i32.const 2))) (call $pop))
                br $args_loop
            ))
        end
    )(else
        loop $pop_loop
            (if (local.get $i) (then
                (local.set $i (i32.sub (local.get $i) (i32.const 1)))
                (drop (call $pop))
                br $pop_loop
            ))
        end
    ))
)
{{end}}


{{- if .HasOp "bufwrite"}}
;;-------------------------------------------------------------------------------
;;   BUFWRITE opcode: writes a frame to a buffer and pops it
;;-------------------------------------------------------------------------------
;;   Mono: pop l and write it
;;   Stereo: pop l r and write them
;;   Writes while the voice is held. WRK[0] is 1 once writing has started
;;   after the note was triggered. Matches bufwrite in vm/go_synth.go.
;;-------------------------------------------------------------------------------
(func $su_op_bufwrite (param $stereo i32) (local $r i32) (local $h i32) (local $l f32) (local $rt f32) (local $cap i32) (local $head i32) (local $ptr i32) (local $fb f32)
    (local.set $r (i32.add (i32.const {{index .Labels "su_buffer_regions"}}) (i32.mul (call $scanOperand) (i32.const 28))))
    (local.set $h (i32.add (i32.const {{index .Labels "su_buffer_headers"}}) (i32.load (local.get $r))))
    (local.set $l (call $pop))
    (local.set $rt (local.get $l))
{{- if .Stereo "bufwrite"}}
    (if (local.get $stereo) (then (local.set $rt (call $pop))))
{{- end}}
    (local.set $cap (i32.load offset=4 (local.get $h)))
    (if (i32.or
            (i32.or (i32.eqz (i32.load (global.get $voice))) (i32.eqz (i32.load offset=4 (global.get $voice))))
            (i32.eqz (local.get $cap))) (then
        return
    ))
    (if (i32.eqz (i32.load (global.get $WRK))) (then
        (i32.store (global.get $WRK) (i32.const 1))
        (if (i32.eqz (i32.and (i32.load offset=24 (local.get $r)) (i32.const 4))) (then ;; once: a new recording
            (i32.store offset=12 (local.get $h) (i32.const 0))
            (i32.store offset=16 (local.get $h) (i32.const 0))
        ))
    ))
    (local.set $head (i32.load offset=12 (local.get $h)))
    (if (i32.ge_u (local.get $head) (local.get $cap)) (then
        return ;; a recording that reached the end
    ))
    (local.set $fb (call $input (i32.const {{.InputNumber "bufwrite" "feedback"}})))
    (local.set $ptr (i32.add
        (i32.const {{index .Labels "su_buffers"}})
        (i32.add
            (i32.load (local.get $h))
            (i32.shl (i32.mul (local.get $head) (i32.load offset=8 (local.get $h))) (i32.const 2))
        )
    ))
    (if (i32.eq (i32.load offset=8 (local.get $h)) (i32.const 2)) (then
        (f32.store (local.get $ptr) (f32.add (f32.mul (f32.load (local.get $ptr)) (local.get $fb)) (local.get $l)))
        (f32.store offset=4 (local.get $ptr) (f32.add (f32.mul (f32.load offset=4 (local.get $ptr)) (local.get $fb)) (local.get $rt)))
    )(else
        (f32.store (local.get $ptr) (f32.add
            (f32.mul (f32.load (local.get $ptr)) (local.get $fb))
            (f32.mul (f32.add (local.get $l) (local.get $rt)) (f32.const 0.5))
        ))
    ))
    (local.set $head (i32.add (local.get $head) (i32.const 1)))
    (if (i32.and (i32.load offset=24 (local.get $r)) (i32.const 4)) (then ;; ring
        (i32.store offset=12 (local.get $h) (i32.rem_u (local.get $head) (local.get $cap)))
        (i32.store offset=16 (local.get $h) (select
            (local.get $cap)
            (i32.add (i32.load offset=16 (local.get $h)) (i32.const 1))
            (i32.ge_u (i32.load offset=16 (local.get $h)) (local.get $cap))
        ))
    )(else
        (i32.store offset=12 (local.get $h) (local.get $head))
        (i32.store offset=16 (local.get $h) (local.get $head))
    ))
)
{{end}}
