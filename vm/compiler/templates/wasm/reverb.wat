{{- if .HasOp "reverb"}}
;;-------------------------------------------------------------------------------
;;   REVERB opcode: replaces the stereo signal on top of the stack with its
;;   reverb, as reverb in vm/reverb.go, where it is explained
;;-------------------------------------------------------------------------------
;;   The state of the unit is at $reverbWRK, in su_reverb: the sample t, Σx/4
;;   of the outputs of the lines, the states of the low cut (8) and the high
;;   cut (16) of left and right, the outputs of the 8 lines (32), the phases
;;   of their modulation (64) and the states of their decay filters (96,
;;   128); from 160 the rings of the diffuser, each half the bytes of the one
;;   before: the stereo ring of 2^14 frames, three rings of frames of 8
;;   channels, and one for the frame of the diffused input; then the ring of
;;   the network, 2^14 frames of 8 channels. The constants of the unit are at
;;   k: the coefficients A, B and C of the decay of the lines, their lengths
;;   (96) and the taps of the diffuser (128).
;;-------------------------------------------------------------------------------
(func $su_op_reverb (param $stereo i32) (local $s i32) (local $k i32) (local $t i32) (local $c i32) (local $n i32) (local $r i32) (local $m i32) (local $i i32) (local $d i32) (local $q i32) (local $z i32) (local $e i32) (local $w i32)
    (local $a f32) (local $b f32) (local $x f32) (local $y f32) (local $f f32) (local $depth f32)
    (local.set $s (global.get $reverbWRK))
    (local.set $k (i32.add (i32.mul (call $scanOperand) (i32.const 192)) (i32.const {{index .Labels "su_reverb_consts"}})))
    (local.set $t (i32.load (local.get $s)))
    ;; the input, times the gain, through the low cut and the high cut into
    ;; the stereo ring
    (local.set $a (call $reverbCoef (i32.const {{.InputNumber "reverb" "lowcut"}})))
    (local.set $b (call $reverbCoef (i32.const {{.InputNumber "reverb" "highcut"}})))
    loop $input
        (local.set $x (f32.mul (f32.load (local.tee $d (i32.add (global.get $sp) (local.get $c)))) (f32.const 2.3713737)))
        (f32.store offset=8 (local.tee $q (i32.add (local.get $s) (local.get $c))) (local.tee $y (f32.add
            (local.tee $y (f32.load offset=8 (local.get $q)))
            (f32.mul (local.get $a) (f32.sub (local.get $x) (local.get $y))))))
        (local.set $x (f32.sub (local.get $x) (local.get $y)))
        (f32.store offset=16 (local.get $q) (local.tee $y (f32.add
            (local.tee $y (f32.load offset=16 (local.get $q)))
            (f32.mul (local.get $b) (f32.sub (local.get $x) (local.get $y))))))
        (f32.store offset=160 (i32.add (local.get $q) (i32.and (local.tee $i (i32.shl (local.get $t) (i32.const 3))) (i32.const 0x1ffff))) (local.get $y))
        (f32.store (local.get $d) (f32.const 0))
        (br_if $input (local.tee $c (i32.xor (local.get $c) (i32.const 4))))
    end
    ;; the diffuser: each step reads the 8 channels from its ring r of m+1
    ;; bytes, where frame t is at i, delayed and shuffled, into frame t of
    ;; the next ring, d, and mixes them there
    (local.set $r (i32.add (local.get $s) (i32.const 160)))
    (local.set $m (i32.const 0x1ffff))
    loop $steps
        (local.set $d (i32.add
            (local.tee $q (i32.add (i32.add (local.get $r) (local.get $m)) (i32.const 1)))
            (i32.and (local.tee $z (i32.shl (local.get $t) (i32.const 5))) (local.tee $e (i32.shr_u (local.get $m) (i32.const 1))))))
        loop $taps
            ;; a tap: how far behind frame t it reads, in floats, twice, and
            ;; in bit 0 whether it flips the sign
            (i32.store (i32.add (local.get $d) (i32.and (local.get $n) (i32.const 28)))
                (i32.xor
                    (i32.load (i32.add (local.get $r) (i32.and
                        (i32.sub (local.get $i) (i32.shl
                            (i32.shr_u (local.tee $w (i32.load16_u offset=128 (i32.add (local.get $k) (i32.shr_u (local.get $n) (i32.const 1))))) (i32.const 1))
                            (i32.const 2)))
                        (local.get $m))))
                    (i32.shl (local.get $w) (i32.const 31))))
            (br_if $taps (i32.and (local.tee $n (i32.add (local.get $n) (i32.const 4))) (i32.const 28)))
        end
        ;; the Hadamard mix, scaled by 1/√8: the butterflies of channels 4,
        ;; 2 and 1 apart
        (local.set $w (i32.const 16))
        loop $passes
            (local.set $c (i32.const 0))
            loop $channels
                (if (i32.eqz (i32.and (local.get $c) (local.get $w))) (then
                    (local.set $x (f32.load (local.tee $r (i32.add (local.get $d) (local.get $c)))))
                    (local.set $y (f32.load (local.tee $i (i32.add (local.get $r) (local.get $w)))))
                    (f32.store (local.get $r) (f32.add (local.get $x) (local.get $y)))
                    (f32.store (local.get $i) (f32.sub (local.get $x) (local.get $y)))
                ))
                (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 4))) (i32.const 32)))
            end
            (br_if $passes (i32.gt_u (local.tee $w (i32.shr_u (local.get $w) (i32.const 1))) (i32.const 2)))
        end
        loop $scale
            (f32.store
                (local.tee $r (i32.add (local.get $d) (local.tee $c (i32.sub (local.get $c) (i32.const 4)))))
                (f32.mul (f32.load (local.get $r)) (f32.const 0.35355338)))
            (br_if $scale (local.get $c))
        end
        (local.set $r (local.get $q))
        (local.set $m (local.get $e))
        (local.set $i (local.get $z))
        (br_if $steps (i32.lt_u (local.get $n) (i32.const 128)))
    end
    ;; the network: each line is fed the diffused input plus the Householder
    ;; mix of the outputs of the lines in the last sample, and read at its
    ;; length plus the modulation, through its decay filter. r is channel c
    ;; of the first frame of its ring, z the byte offset of frame t.
    (local.set $r (i32.add (local.get $r) (i32.const 0x2000)))
{{- if .ReverbMod}}
    (local.set $depth (f32.mul (f32.mul (local.tee $x (call $input (i32.const {{.InputNumber "reverb" "mod"}}))) (local.get $x)) (f32.const 352.8)))
{{- end}}
    loop $lines
        (f32.store (i32.add (local.get $r) (i32.and (local.get $z) (i32.const 0x7ffff))) (f32.add
            (f32.load (i32.add (local.get $d) (local.get $c)))
            (f32.sub (f32.load offset=32 (local.tee $q (i32.add (local.get $s) (local.get $c)))) (f32.load offset=4 (local.get $s)))))
        (local.set $x (f32.load offset=96 (local.tee $n (i32.add (local.get $k) (local.get $c)))))
{{- if .ReverbMod}}
        ;; the phase advances at 1 + c/8 times the rate, and the triangle
        ;; |2·frac(phase + c/8) - 1| times the depth is added to the length
        (f32.store offset=64 (local.get $q) (local.tee $f (f32.sub
            (local.tee $f (f32.add
                (f32.load offset=64 (local.get $q))
                (f32.mul (f32.const 1.603417e-05) (f32.add (local.tee $y (f32.mul (f32.convert_i32_u (local.get $c)) (f32.const 0.03125))) (f32.const 1)))))
            (f32.floor (local.get $f)))))
        (local.set $x (f32.min (f32.max (f32.add (local.get $x) (f32.mul (local.get $depth) (f32.abs (f32.sub
            (f32.mul (f32.sub (local.tee $f (f32.add (local.get $f) (local.get $y))) (f32.floor (local.get $f))) (f32.const 2))
            (f32.const 1))))) (f32.const 1)) (f32.const 16382)))
{{- end}}
        (local.set $f (f32.sub (local.get $x) (f32.convert_i32_s (local.tee $i (i32.trunc_sat_f32_s (local.get $x))))))
        (local.set $y (f32.add
            (local.tee $y (f32.load (i32.add (local.get $r) (i32.and (local.tee $i (i32.sub (local.get $z) (i32.shl (local.get $i) (i32.const 5)))) (i32.const 0x7ffff)))))
            (f32.mul (local.get $f) (f32.sub
                (f32.load (i32.add (local.get $r) (i32.and (i32.sub (local.get $i) (i32.const 32)) (i32.const 0x7ffff))))
                (local.get $y)))))
        ;; the decay filter: lo += 0.035·(y - lo), y += A·lo,
        ;; hi += 0.348·(y - hi), out = B·y + C·hi
        (f32.store offset=96 (local.get $q) (local.tee $f (f32.add
            (local.tee $f (f32.load offset=96 (local.get $q)))
            (f32.mul (f32.const 0.034992073) (f32.sub (local.get $y) (local.get $f))))))
        (local.set $y (f32.add (local.get $y) (f32.mul (f32.load (local.get $n)) (local.get $f))))
        (f32.store offset=128 (local.get $q) (local.tee $f (f32.add
            (local.tee $f (f32.load offset=128 (local.get $q)))
            (f32.mul (f32.const 0.34781536) (f32.sub (local.get $y) (local.get $f))))))
        (f32.store offset=32 (local.get $q) (f32.add
            (f32.mul (f32.load offset=32 (local.get $n)) (local.get $y))
            (f32.mul (f32.load offset=64 (local.get $n)) (local.get $f))))
        (local.set $r (i32.add (local.get $r) (i32.const 4)))
        (br_if $lines (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 4))) (i32.const 32)))
    end
    (f32.store offset=4 (local.get $s) (f32.mul
        (f32.add
            (f32.add (call $reverbHalf (local.tee $q (i32.add (local.get $s) (i32.const 32)))) (call $reverbHalf (i32.add (local.get $s) (i32.const 40))))
            (f32.add (call $reverbHalf (i32.add (local.get $s) (i32.const 36))) (call $reverbHalf (i32.add (local.get $s) (i32.const 44)))))
        (f32.const 0.25)))
    ;; the early reflections, the diffused input, and the tail, the outputs
    ;; of the lines
    (call $reverbSum (local.get $d) (f32.const 1.25) (f32.const 0.4216965))
    (call $reverbSum (local.get $q) (f32.const 1.5) (f32.const 1))
    (i32.store (local.get $s) (i32.add (local.get $t) (i32.const 1)))
    (global.set $reverbWRK (i32.add (local.get $s) (i32.const 778400)))
)

;; $reverbCoef returns the coefficient of the one-pole filters of the input,
;; as that of mcfilter: 1 - 2^(-2π·20·2^(10f)/44100·log2(e))
(func $reverbCoef (param $input i32) (result f32)
    (f32.sub (f32.const 1) (call $exp2f (f32.mul
        (call $exp2f (f32.mul (call $input (local.get $input)) (f32.const 10)))
        (f32.const -0.004110984))))
)

;; $reverbHalf returns x[c] + x[c+4], for channel c at p
(func $reverbHalf (param $p i32) (result f32)
    (f32.add (f32.load (local.get $p)) (f32.load offset=16 (local.get $p)))
)

;; $reverbSum adds the sum of the 8 channels at p to the stereo signal on top
;; of the stack: the even channels to the left and the odd ones to the right,
;; with the polarities +, +, -, -, the side signal times width, all times gain
(func $reverbSum (param $p i32) (param $width f32) (param $gain f32) (local $l f32) (local $r f32) (local $mid f32)
    (local.set $l (f32.sub (call $reverbHalf (local.get $p)) (call $reverbHalf (i32.add (local.get $p) (i32.const 8)))))
    (local.set $r (f32.sub (call $reverbHalf (i32.add (local.get $p) (i32.const 4))) (call $reverbHalf (i32.add (local.get $p) (i32.const 12)))))
    (local.set $mid (f32.mul (f32.add (local.get $l) (local.get $r)) (f32.const 0.125)))
    (local.set $l (f32.mul (f32.mul (f32.sub (local.get $l) (local.get $r)) (f32.const 0.125)) (local.get $width)))
    (f32.store (global.get $sp) (f32.add (f32.load (global.get $sp)) (f32.mul (f32.add (local.get $mid) (local.get $l)) (local.get $gain))))
    (f32.store offset=4 (global.get $sp) (f32.add (f32.load offset=4 (global.get $sp)) (f32.mul (f32.sub (local.get $mid) (local.get $l)) (local.get $gain))))
)
{{- end}}