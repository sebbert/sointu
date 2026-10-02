{{- if .MCTable}}
;;-------------------------------------------------------------------------------
;;   mc units: they process buses of 8 channels in place, sample by sample, as
;;   two f32x4 vectors, lane by lane like vm/mc.go. They run only in the first
;;   voice of their instrument.
;;
;;   $mcUnit scans the operand of the unit, its index, and returns the address
;;   of its entry in su_mc_table: the offsets of its bus and its state in
;;   su_mc, the offset of its constant data in su_mc_consts, and, when an
;;   instrument with mc units has several voices, the offset of the voice
;;   running it from su_voices.
;;-------------------------------------------------------------------------------
(func $mcUnit (result i32)
{{- if .MCVoices}}
    (i32.add (i32.const {{index .Labels "su_mc_table"}}) (i32.shl (call $scanOperand) (i32.const 4)))
{{- else}}
    (i32.add (i32.const {{index .Labels "su_mc_table"}}) (i32.mul (call $scanOperand) (i32.const 12)))
{{- end}}
)
{{- if .MCVoices}}

;; $mcVoice is true if the current voice runs the unit
(func $mcVoice (param $u i32) (result i32)
    (i32.eq (i32.sub (global.get $voice) (i32.const {{index .Labels "su_voices"}})) (i32.load offset=12 (local.get $u)))
)
{{- end}}

;; $mcBus returns the address of the unit's bus
(func $mcBus (param $u i32) (result i32)
    (i32.add (i32.const {{index .Labels "su_mc"}}) (i32.load (local.get $u)))
)
{{- if or .MCSpreadGain .MCSumGain}}

;; $mcGain returns the gain of mcspread and mcsum, ±40 dB like dbgain
(func $mcGain (param $p f32) (result f32)
    (call $exp2f (f32.mul (f32.sub (local.get $p) (f32.const 0.5)) (f32.const 13.287712379549449)))
)
{{- end}}
{{- end}}

{{- if .HasOp "mcspread"}}
;;-------------------------------------------------------------------------------
;;   MCSPREAD opcode: pops a signal, or left and right, and spreads it over
;;   the bus: channel c gets left (even c) or right (odd c) times the gain,
;;   negated for c = 2, 3, 6, 7. With add, it adds to the bus.
;;-------------------------------------------------------------------------------
(func $su_op_mcspread (param $stereo i32) (local $u i32) (local $b i32) (local $add i32) (local $l f32) (local $r f32) (local $v v128)
    (local.set $r (local.tee $l (call $pop)))
{{- if .Stereo "mcspread"}}
    (if (local.get $stereo) (then
        (local.set $r (call $pop))
    ))
{{- end}}
    (local.set $u (call $mcUnit))
{{- if .MCSpreadAddOperand}}
    (local.set $add (call $scanOperand)) ;; add
{{- end}}
{{- if .MCVoices}}
    (if (i32.eqz (call $mcVoice (local.get $u))) (then
        return
    ))
{{- end}}
{{- if .Stereo "mcspread"}}
    (local.set $v (f32x4.replace_lane 3 (f32x4.replace_lane 1 (f32x4.splat (local.get $l)) (local.get $r)) (local.get $r)))
{{- else}}
    (local.set $v (f32x4.splat (local.get $l)))
{{- end}}
{{- if .MCSpreadGain}}
    ;; the gain of the spread
    (local.set $v (f32x4.mul (local.get $v) (f32x4.splat (call $mcGain (call $input (i32.const {{.InputNumber "mcspread" "gain"}}))))))
{{- end}}
    (local.set $v (f32x4.mul (local.get $v) (v128.const f32x4 1 1 -1 -1)))
    (local.set $b (call $mcBus (local.get $u)))
{{- if .MCSpreadAddOperand}}
    (if (local.get $add) (then
        (v128.store (local.get $b) (f32x4.add (v128.load (local.get $b)) (local.get $v)))
        (v128.store offset=16 (local.get $b) (f32x4.add (v128.load offset=16 (local.get $b)) (local.get $v)))
        return
    ))
{{- end}}
    (v128.store (local.get $b) (local.get $v))
    (v128.store offset=16 (local.get $b) (local.get $v))
)
{{- end}}

{{- if .HasOp "mcsum"}}
;;-------------------------------------------------------------------------------
;;   MCSUM opcode: pushes the sum of the bus, with the polarities of
;;   mcspread: in stereo, left from the even channels and right from the odd
;;   ones, with width; in mono, all of them.
;;-------------------------------------------------------------------------------
(func $su_op_mcsum (param $stereo i32) (local $u i32) (local $b i32) (local $h v128) (local $l f32) (local $r f32) (local $g f32) (local $mid f32) (local $side f32)
    (local.set $u (call $mcUnit))
{{- if .MCVoices}}
    (if (call $mcVoice (local.get $u)) (then
{{- end}}
        (local.set $b (call $mcBus (local.get $u)))
        (local.set $h (f32x4.add (v128.load (local.get $b)) (v128.load offset=16 (local.get $b))))
        (local.set $l (f32.sub (f32x4.extract_lane 0 (local.get $h)) (f32x4.extract_lane 2 (local.get $h))))
        (local.set $r (f32.sub (f32x4.extract_lane 1 (local.get $h)) (f32x4.extract_lane 3 (local.get $h))))
{{- if .MCSumGain}}
        ;; the gain of the sum
        (local.set $g (call $mcGain (call $input (i32.const {{.InputNumber "mcsum" "gain"}}))))
{{- end}}
        (local.set $mid (f32.mul (f32.add (local.get $l) (local.get $r)) (f32.const 0.125)))
{{- if .Stereo "mcsum"}}
        (if (local.get $stereo) (then
{{- if .MCSumWidth}}
            (local.set $side (f32.mul
                (f32.mul (f32.sub (local.get $l) (local.get $r)) (f32.const 0.125))
                (f32.mul (call $input (i32.const {{.InputNumber "mcsum" "width"}})) (f32.const 2))))
{{- else}}
            (local.set $side (f32.mul (f32.sub (local.get $l) (local.get $r)) (f32.const 0.125)))
{{- end}}
            (local.set $r {{if .MCSumGain}}(f32.mul (f32.sub (local.get $mid) (local.get $side)) (local.get $g)){{else}}(f32.sub (local.get $mid) (local.get $side)){{end}})
        ))
{{- end}}
        (local.set $l {{if .MCSumGain}}(f32.mul (f32.add (local.get $mid) (local.get $side)) (local.get $g)){{else}}(f32.add (local.get $mid) (local.get $side)){{end}})
{{- if .MCVoices}}
    )(else
        (local.set $r (f32.const 0))
    ))
{{- end}}
{{- if .Stereo "mcsum"}}
    (if (local.get $stereo) (then
        (call $push (local.get $r))
    ))
{{- end}}
    (call $push (local.get $l))
)
{{- end}}

{{- if .HasOp "mcmix"}}
;;-------------------------------------------------------------------------------
;;   MCMIX opcode: mixes the bus with a Hadamard matrix scaled by 1/√8, a
;;   Householder reflection x - (2/8)·Σx, or a shuffle: channel c becomes the
;;   channel at the byte offset in the constants times its sign. The shuffle
;;   gathers into the unit's own state, which it does not otherwise use.
;;-------------------------------------------------------------------------------
{{- if .SupportsParamValue "mcmix" "type" 0}}
;; $mcHadamard4 does the butterflies of lanes 2 and 1 apart and the scaling
(func $mcHadamard4 (param $x v128) (result v128)
    (local.set $x (f32x4.add
        (i8x16.shuffle 0 1 2 3 4 5 6 7 0 1 2 3 4 5 6 7 (local.get $x) (local.get $x))
        (f32x4.mul (i8x16.shuffle 8 9 10 11 12 13 14 15 8 9 10 11 12 13 14 15 (local.get $x) (local.get $x)) (v128.const f32x4 1 1 -1 -1))))
    (local.set $x (f32x4.add
        (i8x16.shuffle 0 1 2 3 0 1 2 3 8 9 10 11 8 9 10 11 (local.get $x) (local.get $x))
        (f32x4.mul (i8x16.shuffle 4 5 6 7 4 5 6 7 12 13 14 15 12 13 14 15 (local.get $x) (local.get $x)) (v128.const f32x4 1 -1 1 -1))))
    (f32x4.mul (local.get $x) (v128.const f32x4 0.35355338 0.35355338 0.35355338 0.35355338))
)
{{- end}}

(func $su_op_mcmix (param $stereo i32) (local $u i32) (local $t i32) (local $b i32) (local $k i32) (local $c i32) (local $lo v128) (local $hi v128) (local $x v128)
    (local.set $u (call $mcUnit))
{{- if .MCMixTypeOperand}}
    (local.set $t (call $scanOperand)) ;; the type of the mix
{{- end}}
{{- if .MCVoices}}
    (if (i32.eqz (call $mcVoice (local.get $u))) (then
        return
    ))
{{- end}}
    (local.set $b (call $mcBus (local.get $u)))
    (local.set $lo (v128.load (local.get $b)))
    (local.set $hi (v128.load offset=16 (local.get $b)))
{{- /* without the operand, all mcmix units have the same type: its code alone */}}
{{- if .SupportsParamValue "mcmix" "type" 0}}
{{- if .MCMixTypeOperand}}
    (if (i32.eqz (local.get $t)) (then
{{- end}}
        (local.set $x (f32x4.add (local.get $lo) (local.get $hi)))
        (v128.store offset=16 (local.get $b) (call $mcHadamard4 (f32x4.sub (local.get $lo) (local.get $hi))))
        (v128.store (local.get $b) (call $mcHadamard4 (local.get $x)))
{{- if .MCMixTypeOperand}}
        return
    ))
{{- end}}
{{- end}}
{{- if .SupportsParamValue "mcmix" "type" 2}}
{{- if .MCMixTypeOperand}}
    (if (i32.eq (local.get $t) (i32.const 2)) (then
{{- end}}
        (local.set $k (i32.add (i32.const {{index .Labels "su_mc_consts"}}) (i32.load offset=8 (local.get $u))))
        loop $channels
            (f32.store (i32.add (global.get $WRK) (local.get $c)) (f32.mul
                (f32.load (i32.add (local.get $b) (i32.load (i32.add (local.get $k) (local.get $c)))))
                (f32.load offset=32 (i32.add (local.get $k) (local.get $c)))))
            (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 4))) (i32.const 32)))
        end
        (v128.store (local.get $b) (v128.load (global.get $WRK)))
        (v128.store offset=16 (local.get $b) (v128.load offset=16 (global.get $WRK)))
{{- if .MCMixTypeOperand}}
        return
    ))
{{- end}}
{{- end}}
{{- if .SupportsParamValue "mcmix" "type" 1}}
    ;; Householder: the sum in every lane, ((x0+x4)+(x2+x6)) + ((x1+x5)+(x3+x7))
    (local.set $x (f32x4.add (local.get $lo) (local.get $hi)))
    (local.set $x (f32x4.add (local.get $x) (i8x16.shuffle 8 9 10 11 12 13 14 15 0 1 2 3 4 5 6 7 (local.get $x) (local.get $x))))
    (local.set $x (f32x4.add (local.get $x) (i8x16.shuffle 4 5 6 7 0 1 2 3 12 13 14 15 8 9 10 11 (local.get $x) (local.get $x))))
    (local.set $x (f32x4.mul (local.get $x) (v128.const f32x4 0.25 0.25 0.25 0.25)))
    (v128.store (local.get $b) (f32x4.sub (local.get $lo) (local.get $x)))
    (v128.store offset=16 (local.get $b) (f32x4.sub (local.get $hi) (local.get $x)))
{{- end}}
)
{{- end}}

{{- if .HasOp "mcloop"}}
;;-------------------------------------------------------------------------------
;;   MCLOOP opcode: adds the frame stored by mcloopend in the previous sample,
;;   times feedback, to the bus
;;-------------------------------------------------------------------------------
(func $su_op_mcloop (param $stereo i32) (local $u i32) (local $b i32) (local $f v128)
    (local.set $u (call $mcUnit))
{{- if .MCVoices}}
    (if (call $mcVoice (local.get $u)) (then
{{- end}}
        (local.set $b (call $mcBus (local.get $u)))
{{- if .MCLoopFeedback}}
        (local.set $f (f32x4.splat (call $input (i32.const {{.InputNumber "mcloop" "feedback"}}))))
        (v128.store (local.get $b) (f32x4.add (v128.load (local.get $b)) (f32x4.mul (local.get $f) (v128.load offset=32 (local.get $b)))))
        (v128.store offset=16 (local.get $b) (f32x4.add (v128.load offset=16 (local.get $b)) (f32x4.mul (local.get $f) (v128.load offset=48 (local.get $b)))))
{{- else}}
        ;; the feedback is 1 in the song
        (v128.store (local.get $b) (f32x4.add (v128.load (local.get $b)) (v128.load offset=32 (local.get $b))))
        (v128.store offset=16 (local.get $b) (f32x4.add (v128.load offset=16 (local.get $b)) (v128.load offset=48 (local.get $b))))
{{- end}}
{{- if .MCVoices}}
    ))
{{- end}}
)
{{- end}}

{{- if .HasOp "mcloopend"}}
;;-------------------------------------------------------------------------------
;;   MCLOOPEND opcode: stores the bus for the mcloop of the next sample
;;-------------------------------------------------------------------------------
(func $su_op_mcloopend (param $stereo i32) (local $u i32) (local $b i32)
    (local.set $u (call $mcUnit))
{{- if .MCVoices}}
    (if (call $mcVoice (local.get $u)) (then
{{- end}}
        (local.set $b (call $mcBus (local.get $u)))
        (v128.store offset=32 (local.get $b) (v128.load (local.get $b)))
        (v128.store offset=48 (local.get $b) (v128.load offset=16 (local.get $b)))
{{- if .MCVoices}}
    ))
{{- end}}
)
{{- end}}

{{- if .HasOp "mcfilter"}}
;;-------------------------------------------------------------------------------
;;   MCFILTER opcode: a one-pole low-pass on every channel of the bus, or the
;;   high-pass that is the rest, with a = 1 - 2^(-2π·20·2^(10f)/44100·log2(e))
;;-------------------------------------------------------------------------------
(func $su_op_mcfilter (param $stereo i32) (local $u i32) (local $t i32) (local $b i32) (local $s i32) (local $h i32) (local $a v128) (local $x v128) (local $lo v128)
    (local.set $u (call $mcUnit))
{{- if .MCFilterTypeOperand}}
    (local.set $t (call $scanOperand)) ;; the type of the filter
{{- end}}
{{- if .MCVoices}}
    (if (i32.eqz (call $mcVoice (local.get $u))) (then
        return
    ))
{{- end}}
    (local.set $b (call $mcBus (local.get $u)))
    (local.set $s (i32.add (i32.const {{index .Labels "su_mc"}}) (i32.load offset=4 (local.get $u))))
    (local.set $a (f32x4.splat (f32.sub (f32.const 1) (call $exp2f (f32.mul
        (call $exp2f (f32.mul (call $input (i32.const {{.InputNumber "mcfilter" "frequency"}})) (f32.const 10)))
        (f32.const -0.004110984))))))
    loop $halves
        (local.set $x (v128.load (i32.add (local.get $b) (local.get $h))))
        (local.set $lo (v128.load offset=32 (i32.add (local.get $s) (local.get $h))))
        (local.set $lo (f32x4.add (local.get $lo) (f32x4.mul (local.get $a) (f32x4.sub (local.get $x) (local.get $lo)))))
        (v128.store offset=32 (i32.add (local.get $s) (local.get $h)) (local.get $lo))
{{- if .SupportsParamValue "mcfilter" "type" 1}}
{{- if .MCFilterTypeOperand}}
        (if (local.get $t) (then
{{- end}}
            (local.set $lo (f32x4.sub (local.get $x) (local.get $lo))) ;; high-pass
{{- if .MCFilterTypeOperand}}
        ))
{{- end}}
{{- end}}
        (v128.store (i32.add (local.get $b) (local.get $h)) (local.get $lo))
        (br_if $halves (i32.lt_u (local.tee $h (i32.add (local.get $h) (i32.const 16))) (i32.const 32)))
    end
)
{{- end}}

{{- if .HasOp "mcdelay"}}
;;-------------------------------------------------------------------------------
;;   MCDELAY opcode: delays each channel of the bus by its line, as mcdelay in
;;   vm/mc.go: reads the ring at the channel's length, times 2^((60-note)/12)
;;   with note tracking, plus the modulation (a triangle wave), interpolating
;;   linearly; writes the channel to the ring, or with allpass the channel plus
;;   apgain times what was read, which is then minus apgain times what was
;;   written; and replaces the channel with what was read through the decay
;;   filter. The state is at s: phases, low and high filter states, the
;;   position t, and the ring at s+128; the constants at k: lengths, A, B, C,
;;   the mask of the ring, the longest delay and apgain.
;;-------------------------------------------------------------------------------
(func $su_op_mcdelay (param $stereo i32) (local $u i32) (local $flags i32) (local $b i32) (local $s i32) (local $k i32) (local $ring i32) (local $t i32) (local $mask i32) (local $h i32) (local $p f32)
    (local $rate v128) (local $depth v128) (local $nt v128) (local $max v128) (local $g v128) (local $v v128) (local $d v128) (local $i v128) (local $ia v128) (local $ib v128) (local $ya v128) (local $yb v128) (local $y v128) (local $w v128) (local $f v128)
    (local.set $u (call $mcUnit))
{{- if .MCDelayFlagsOperand}}
    (local.set $flags (call $scanOperand)) ;; note tracking and allpass
{{- end}}
{{- if .MCVoices}}
    (if (i32.eqz (call $mcVoice (local.get $u))) (then
        return
    ))
{{- end}}
    (local.set $b (call $mcBus (local.get $u)))
    (local.set $s (i32.add (i32.const {{index .Labels "su_mc"}}) (i32.load offset=4 (local.get $u))))
    (local.set $k (i32.add (i32.const {{index .Labels "su_mc_consts"}}) (i32.load offset=8 (local.get $u))))
{{- if .MCDelayMod}}
    ;; the rate in turns per sample, the depth in samples
    (local.set $rate (f32x4.splat (f32.mul
        (call $exp2f (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "mcdelay" "modrate"}})) (f32.const 8)) (f32.const 4)))
        (f32.const 2.2675737e-05))))
    (local.set $p (call $input (i32.const {{.InputNumber "mcdelay" "moddepth"}})))
    (local.set $depth (f32x4.splat (f32.mul (f32.mul (local.get $p) (local.get $p)) (f32.const 352.8))))
{{- end}}
{{- if .SupportsParamValue "mcdelay" "notetracking" 1}}
    (local.set $nt (v128.const f32x4 1 1 1 1))
    (if (i32.and (local.get $flags) (i32.const 1)) (then
        (local.set $nt (f32x4.splat (call $exp2f (f32.mul
            (f32.sub (f32.const 60) (f32.convert_i32_u (i32.load (global.get $voice))))
            (f32.const 0.083333336)))))
    ))
{{- end}}
    (local.set $mask (i32.load offset=128 (local.get $k)))
{{- if or .MCDelayMod (.SupportsParamValue "mcdelay" "notetracking" 1)}}
    (local.set $max (f32x4.splat (f32.load offset=132 (local.get $k))))
{{- end}}
{{- if .SupportsParamValue "mcdelay" "allpass" 1}}
    (local.set $g (f32x4.splat (f32.load offset=136 (local.get $k))))
{{- end}}
    (local.set $t (i32.load offset=96 (local.get $s)))
    (local.set $ring (i32.add (local.get $s) (i32.const 128)))
    loop $halves
{{- if .MCDelayMod}}
        ;; the modulation: the phase advances at the channel's rate, and the
        ;; triangle is |2·frac(phase + offset) - 1|
        (local.set $v (f32x4.add
            (v128.load (i32.add (local.get $s) (local.get $h)))
            (f32x4.mul (local.get $rate) (v128.load offset={{index .Labels "su_mc_consts"}} (local.get $h)))))
        (local.set $v (f32x4.sub (local.get $v) (f32x4.floor (local.get $v))))
        (v128.store (i32.add (local.get $s) (local.get $h)) (local.get $v))
        (local.set $v (f32x4.add (local.get $v) (v128.load offset={{add (index .Labels "su_mc_consts") 32}} (local.get $h))))
        (local.set $v (f32x4.abs (f32x4.sub
            (f32x4.mul (f32x4.sub (local.get $v) (f32x4.floor (local.get $v))) (v128.const f32x4 2 2 2 2))
            (v128.const f32x4 1 1 1 1))))
{{- end}}
        (local.set $d (v128.load (i32.add (local.get $k) (local.get $h))))
{{- if .SupportsParamValue "mcdelay" "notetracking" 1}}
        (local.set $d (f32x4.mul (local.get $d) (local.get $nt)))
{{- end}}
{{- if .MCDelayMod}}
        (local.set $d (f32x4.add (local.get $d) (f32x4.mul (local.get $depth) (local.get $v))))
{{- end}}
{{- if or .MCDelayMod (.SupportsParamValue "mcdelay" "notetracking" 1)}}
        ;; a length that is neither modulated nor follows the note is within these already
        (local.set $d (f32x4.min (f32x4.max (local.get $d) (v128.const f32x4 1 1 1 1)) (local.get $max)))
{{- end}}
        (local.set $i (i32x4.trunc_sat_f32x4_s (local.get $d)))
        (local.set $f (f32x4.sub (local.get $d) (f32x4.convert_i32x4_s (local.get $i))))
        ;; the addresses of the frames t-i and t-i-1 of the channels
        (local.set $ia (i32x4.sub (i32x4.splat (local.get $t)) (local.get $i)))
        (local.set $ib (i32x4.sub (local.get $ia) (i32x4.splat (i32.const 1))))
        (local.set $w (i32x4.add (i32x4.splat (local.get $ring)) (v128.load offset={{add (index .Labels "su_mc_consts") .MCChannelOffsets}} (local.get $h))))
        (local.set $ia (i32x4.add (i32x4.shl (v128.and (local.get $ia) (i32x4.splat (local.get $mask))) (i32.const 5)) (local.get $w)))
        (local.set $ib (i32x4.add (i32x4.shl (v128.and (local.get $ib) (i32x4.splat (local.get $mask))) (i32.const 5)) (local.get $w)))
        (local.set $ya (f32x4.splat (f32.load (i32x4.extract_lane 0 (local.get $ia)))))
        (local.set $ya (f32x4.replace_lane 1 (local.get $ya) (f32.load (i32x4.extract_lane 1 (local.get $ia)))))
        (local.set $ya (f32x4.replace_lane 2 (local.get $ya) (f32.load (i32x4.extract_lane 2 (local.get $ia)))))
        (local.set $ya (f32x4.replace_lane 3 (local.get $ya) (f32.load (i32x4.extract_lane 3 (local.get $ia)))))
        (local.set $yb (f32x4.splat (f32.load (i32x4.extract_lane 0 (local.get $ib)))))
        (local.set $yb (f32x4.replace_lane 1 (local.get $yb) (f32.load (i32x4.extract_lane 1 (local.get $ib)))))
        (local.set $yb (f32x4.replace_lane 2 (local.get $yb) (f32.load (i32x4.extract_lane 2 (local.get $ib)))))
        (local.set $yb (f32x4.replace_lane 3 (local.get $yb) (f32.load (i32x4.extract_lane 3 (local.get $ib)))))
        (local.set $y (f32x4.add (local.get $ya) (f32x4.mul (local.get $f) (f32x4.sub (local.get $yb) (local.get $ya)))))
        (local.set $w (v128.load (i32.add (local.get $b) (local.get $h))))
{{- if .SupportsParamValue "mcdelay" "allpass" 1}}
        (if (i32.and (local.get $flags) (i32.const 2)) (then
            (local.set $w (f32x4.add (local.get $w) (f32x4.mul (local.get $g) (local.get $y))))
            (local.set $y (f32x4.sub (local.get $y) (f32x4.mul (local.get $g) (local.get $w))))
        ))
{{- end}}
        (v128.store (i32.add (i32.add (local.get $ring) (i32.shl (local.get $t) (i32.const 5))) (local.get $h)) (local.get $w))
        ;; the decay filter: lo += 0.035·(y - lo), y += A·lo,
        ;; hi += 0.348·(y - hi), out = B·y + C·hi
        (local.set $v (v128.load offset=32 (i32.add (local.get $s) (local.get $h))))
        (local.set $v (f32x4.add (local.get $v) (f32x4.mul (v128.const f32x4 0.034992073 0.034992073 0.034992073 0.034992073) (f32x4.sub (local.get $y) (local.get $v)))))
        (v128.store offset=32 (i32.add (local.get $s) (local.get $h)) (local.get $v))
        (local.set $y (f32x4.add (local.get $y) (f32x4.mul (v128.load offset=32 (i32.add (local.get $k) (local.get $h))) (local.get $v))))
        (local.set $v (v128.load offset=64 (i32.add (local.get $s) (local.get $h))))
        (local.set $v (f32x4.add (local.get $v) (f32x4.mul (v128.const f32x4 0.34781536 0.34781536 0.34781536 0.34781536) (f32x4.sub (local.get $y) (local.get $v)))))
        (v128.store offset=64 (i32.add (local.get $s) (local.get $h)) (local.get $v))
        (v128.store (i32.add (local.get $b) (local.get $h)) (f32x4.add
            (f32x4.mul (v128.load offset=64 (i32.add (local.get $k) (local.get $h))) (local.get $y))
            (f32x4.mul (v128.load offset=96 (i32.add (local.get $k) (local.get $h))) (local.get $v))))
        (br_if $halves (i32.lt_u (local.tee $h (i32.add (local.get $h) (i32.const 16))) (i32.const 32)))
    end
    (i32.store offset=96 (local.get $s) (i32.and (i32.add (local.get $t) (i32.const 1)) (local.get $mask)))
)
{{- end}}