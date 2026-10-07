{{- if .HasOp "convolution"}}
{{- $headers := index .Labels "su_buffer_headers"}}
{{- $x := add (index .Labels "su_spectral") .SpectralScratch}}
{{- $acc := index .Labels "su_conv"}}
{{- $old := add $acc .ConvSpectrum}}
{{- $diff := add $old .ConvSpectrum}}
;;-------------------------------------------------------------------------------
;;   CONVOLUTION opcode: convolves the signal with an impulse response read
;;   from a buffer, as convolution in vm/convolution.go, where it is explained
;;-------------------------------------------------------------------------------
;;   Mono:   x   ->  x*h
;;   Stereo: l r ->  l*h_left r*h_right
;;-------------------------------------------------------------------------------
;;   The state of the unit is at $convWRK, in su_conv: the sample (8 bytes),
;;   and for each channel the ring of the input, the ring of the output
;;   (65536), the head of the response (131072), and from 131328 for each
;;   level the spectra of its partitions and then as many of the last blocks
;;   of the input. A spectrum of a level with the block size b is the real
;;   parts of bins 0 to b and, from 4b + 16, their imaginary parts. The
;;   constants of the unit are at k: the offset of the header of its buffer,
;;   the first frame and the length of the response, the size of a channel of
;;   the state, the partitions of the three levels, and what the units of the
;;   song differ in. The FFTs are computed in the scratch space of the
;;   spectral units.
;;
;;   $convRead copies $len frames of the response of channel $c, from frame
;;   $a on, to $dst, $step bytes apart: silence beyond the length of the
;;   response and the valid frames of the buffer.
;;-------------------------------------------------------------------------------
(func $convRead (param $k i32) (param $c i32) (param $a i32) (param $len i32) (param $dst i32) (param $step i32) (local $h i32) (local $f i32) (local $v f32)
    (local.set $h (i32.load (local.get $k)))
    loop $frames
        (local.set $v (f32.const 0))
        (local.set $f (i32.add (i32.load offset=4 (local.get $k)) (local.get $a)))
        (if (i32.and
            (i32.lt_u (local.get $a) (i32.load offset=8 (local.get $k)))
            (i32.lt_u (local.get $f) (i32.load offset={{add $headers 16}} (local.get $h)))) (then
            (local.set $v (f32.load offset={{index .Labels "su_buffers"}} (i32.add
                (i32.load offset={{$headers}} (local.get $h))
                (i32.shl (i32.add
                    (i32.mul (local.get $f) (i32.load offset={{add $headers 8}} (local.get $h)))
                    (i32.and (local.get $c) (i32.sub (i32.load offset={{add $headers 8}} (local.get $h)) (i32.const 1)))) (i32.const 2)))))
        ))
        (f32.store (local.get $dst) (local.get $v))
        (local.set $dst (i32.add (local.get $dst) (local.get $step)))
        (local.set $a (i32.add (local.get $a) (i32.const 1)))
        (br_if $frames (local.tee $len (i32.sub (local.get $len) (i32.const 1))))
    end
)
{{- if .ConvFFT}}

;; $convSplit stores bins 0 to $b of the spectrum in the scratch space,
;; interleaved, at $dst: the real parts and then the imaginary parts
(func $convSplit (param $dst i32) (param $b i32) (local $k i32) (local $p i32)
    loop $bins
        (f32.store (local.tee $p (i32.add (local.get $dst) (i32.shl (local.get $k) (i32.const 2))))
            (f32.load offset={{$x}} (i32.shl (local.get $k) (i32.const 3))))
        (f32.store offset=16 (i32.add (local.get $p) (i32.shl (local.get $b) (i32.const 2)))
            (f32.load offset={{add $x 4}} (i32.shl (local.get $k) (i32.const 3))))
        (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $b)))
    end
)
{{- if or .ConvLoad .ConvScan}}

;; $convLoad reads partition $p of the level with the block size $b from
;; the buffer and stores its spectrum at $dst
(func $convLoad (param $k i32) (param $c i32) (param $b i32) (param $p i32) (param $dst i32)
    (memory.fill (i32.const {{$x}}) (i32.const 0) (i32.shl (local.get $b) (i32.const 4)))
    (call $convRead (local.get $k) (local.get $c) (i32.mul (local.get $b) (i32.add (local.get $p) (i32.const 1))) (local.get $b) (i32.const {{$x}}) (i32.const 8))
    (call $fft (i32.const {{$x}}) (i32.shl (local.get $b) (i32.const 1)))
    (call $convSplit (local.get $dst) (local.get $b))
)
{{- end}}

;; $convMultiply adds the product of the spectra at $xs and $hs to the one
;; at $acc{{if .ConvScan}}, or with $old that of $xs and what $hs differs from $old in{{end}}
(func $convMultiply (param $acc i32) (param $xs i32) (param $hs i32) (param $old i32) (param $b i32) (local $j i32) (local $o i32) (local $p i32) (local $xr v128) (local $xi v128) (local $hr v128) (local $hi v128)
    (local.set $o (i32.shl (i32.add (local.get $b) (i32.const 4)) (i32.const 2)))
    loop $bins
        (local.set $hr (v128.load (local.tee $p (i32.add (local.get $hs) (local.get $j)))))
        (local.set $hi (v128.load (i32.add (local.get $p) (local.get $o))))
{{- if .ConvScan}}
        (if (local.get $old) (then
            (local.set $hr (f32x4.sub (local.get $hr) (v128.load (local.tee $p (i32.add (local.get $old) (local.get $j))))))
            (local.set $hi (f32x4.sub (local.get $hi) (v128.load (i32.add (local.get $p) (local.get $o)))))
        ))
{{- end}}
        (local.set $xr (v128.load (local.tee $p (i32.add (local.get $xs) (local.get $j)))))
        (local.set $xi (v128.load (i32.add (local.get $p) (local.get $o))))
        (v128.store (local.tee $p (i32.add (local.get $acc) (local.get $j))) (f32x4.add (v128.load (local.get $p))
            (f32x4.sub (f32x4.mul (local.get $xr) (local.get $hr)) (f32x4.mul (local.get $xi) (local.get $hi)))))
        (v128.store (local.tee $p (i32.add (local.get $p) (local.get $o))) (f32x4.add (v128.load (local.get $p))
            (f32x4.add (f32x4.mul (local.get $xr) (local.get $hi)) (f32x4.mul (local.get $xi) (local.get $hr)))))
        (br_if $bins (i32.lt_u (local.tee $j (i32.add (local.get $j) (i32.const 16))) (local.get $o)))
    end
)

;; $convBack transforms the spectrum at $acc back and adds the $b samples
;; that the level puts out to the ring $out from frame $at on: sample i times
;; $base + i·$step
(func $convBack (param $acc i32) (param $out i32) (param $at i32) (param $b i32) (param $base f32) (param $step f32) (local $k i32) (local $p i32) (local $re f32) (local $im f32)
    ;; the conjugate of the spectrum, and the bins above b, which mirror
    ;; those below it; bin b is written last, as the conjugate
    loop $bins
        (local.set $re (f32.load (local.tee $p (i32.add (local.get $acc) (i32.shl (local.get $k) (i32.const 2))))))
        (local.set $im (f32.load offset=16 (i32.add (local.get $p) (i32.shl (local.get $b) (i32.const 2)))))
        (f32.store offset={{$x}} (local.tee $p (i32.shl (i32.sub (i32.shl (local.get $b) (i32.const 1)) (local.get $k)) (i32.const 3))) (local.get $re))
        (f32.store offset={{add $x 4}} (local.get $p) (local.get $im))
        (f32.store offset={{$x}} (local.tee $p (i32.shl (local.get $k) (i32.const 3))) (local.get $re))
        (f32.store offset={{add $x 4}} (local.get $p) (f32.neg (local.get $im)))
        (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $b)))
    end
    (call $fft (i32.const {{$x}}) (i32.shl (local.get $b) (i32.const 1)))
    (local.set $k (i32.const 0))
    loop $samples
        (f32.store (local.tee $p (i32.add (local.get $out) (i32.shl (i32.and (i32.add (local.get $at) (local.get $k)) (i32.const 16383)) (i32.const 2))))
            (f32.add (f32.load (local.get $p)) (f32.mul
                (f32.load offset={{$x}} (i32.shl (i32.add (local.get $b) (local.get $k)) (i32.const 3)))
                (f32.add (f32.mul (f32.convert_i32_u (local.get $k)) (local.get $step)) (local.get $base)))))
        (br_if $samples (i32.lt_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $b)))
    end
)
{{- end}}

(func $su_op_convolution (param $stereo i32) (local $s i32) (local $k i32) (local $n i32) (local $c i32) (local $q i32) (local $i i32) (local $p i32) (local $m i32) (local $x f32) (local $y f32) (local $sum f32)
{{- if .ConvFFT}} (local $lp i32) (local $l i32) (local $b i32) (local $parts i32) (local $sz i32) (local $j i32) (local $qq i32) (local $xs i32) (local $at i32) (local $scale f32){{end}}
{{- if .ConvGain}} (local $gain f32){{end}}
    (local.set $k (i32.add (i32.mul (call $scanOperand) (i32.const {{.ConvRecord}})) (i32.const {{index .Labels "su_conv_consts"}})))
    (local.set $n (i32.load (local.tee $s (global.get $convWRK))))
    (local.set $q (i32.add (local.get $s) (i32.const 8)))
{{- if .ConvGain}}
    (local.set $gain (call $pow2 (f32.mul (f32.sub (call $input (i32.const {{.InputNumber "convolution" "gain"}})) (f32.const 0.5)) (f32.const 13.287712379549449))))
{{- end}}
    loop $channels
{{- if .ConvFFT}}
        ;; the levels: at lp the spectra of the partitions of level l, with
        ;; the block size b, and at xs those of the input
        (local.set $lp (i32.add (local.get $q) (i32.const 131328)))
        (local.set $l (i32.const 0))
        (local.set $b (i32.const 64))
        loop $levels
            (local.set $sz (i32.shl (i32.add (local.get $b) (i32.const 4)) (i32.const 3)))
            (if (local.tee $parts (i32.load offset=16 (i32.add (local.get $k) (local.get $l)))) (then
                (local.set $xs (i32.add (local.get $lp) (i32.mul (local.get $parts) (local.get $sz))))
                (if (i32.eqz (i32.and (local.get $n) (i32.sub (local.get $b) (i32.const 1)))) (then
{{- if .ConvLoad}}
                    ;; the response of a sample is read at the first sample
                    (if (i32.eqz (local.get $n)) (then
                        (local.set $p (i32.const 0))
                        loop $load
                            (call $convLoad (local.get $k) (local.get $c) (local.get $b) (local.get $p) (i32.add (local.get $lp) (i32.mul (local.get $p) (local.get $sz))))
                            (br_if $load (i32.lt_u (local.tee $p (i32.add (local.get $p) (i32.const 1))) (local.get $parts)))
                        end
                    ))
{{- end}}
                    ;; the spectrum of the last two blocks of the input
                    (local.set $i (i32.const 0))
                    loop $input
                        (i64.store offset={{$x}} (i32.shl (local.get $i) (i32.const 3)) (i64.load32_u (i32.add (local.get $q) (i32.shl
                            (i32.and (i32.add (i32.sub (local.get $n) (i32.shl (local.get $b) (i32.const 1))) (local.get $i)) (i32.const 16383))
                            (i32.const 2)))))
                        (br_if $input (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (i32.shl (local.get $b) (i32.const 1))))
                    end
                    (call $fft (i32.const {{$x}}) (i32.shl (local.get $b) (i32.const 1)))
                    (local.set $qq (i32.rem_u (local.tee $j (i32.div_u (local.get $n) (local.get $b))) (local.get $parts)))
                    (call $convSplit (i32.add (local.get $xs) (i32.mul (local.get $qq) (local.get $sz))) (local.get $b))
                    ;; the sum over the partitions of the spectrum of the
                    ;; block p blocks ago times that of partition p
                    (memory.fill (i32.const {{$acc}}) (i32.const 0) (local.get $sz))
                    (local.set $p (i32.const 0))
                    loop $partitions
                        (call $convMultiply (i32.const {{$acc}})
                            (i32.add (local.get $xs) (i32.mul (i32.rem_u (i32.sub (i32.add (local.get $qq) (local.get $parts)) (local.get $p)) (local.get $parts)) (local.get $sz)))
                            (i32.add (local.get $lp) (i32.mul (local.get $p) (local.get $sz)))
                            (i32.const 0) (local.get $b))
                        (br_if $partitions (i32.lt_u (local.tee $p (i32.add (local.get $p) (i32.const 1))) (local.get $parts)))
                    end
                    (local.set $scale (f32.div (f32.const 0.5) (f32.convert_i32_u (local.get $b))))
                    (local.set $at {{if .ConvPredelay}}(i32.add (local.get $n) (i32.load offset={{.ConvPredelayAt}} (local.get $k))){{else}}(local.get $n){{end}})
                    (call $convBack (i32.const {{$acc}}) (i32.add (local.get $q) (i32.const 65536)) (local.get $at) (local.get $b) (local.get $scale) (f32.const 0))
{{- if .ConvScan}}
                    ;; the partitions that the scan passes are read again,
                    ;; and what that changes in this block is added
                    (memory.fill (i32.const {{$diff}}) (i32.const 0) (local.get $sz))
{{- if .ConvFollow}}
                    (local.set $i (i32.const 0))
                    loop $scan
                        (local.set $p (i32.rem_u
                            (i32.add (i32.mul
                                (i32.sub (i32.add (local.get $j) (i32.shl (local.get $parts) (i32.const 1))) (i32.const 2))
                                (i32.load offset={{.ConvFollowAt}} (local.get $k))) (local.get $i))
                            (local.get $parts)))
{{- else}}
                        (local.set $p (i32.rem_u (i32.sub (i32.add (local.get $j) (i32.shl (local.get $parts) (i32.const 1))) (i32.const 2)) (local.get $parts)))
{{- end}}
                        (memory.copy (i32.const {{$old}}) (local.tee $m (i32.add (local.get $lp) (i32.mul (local.get $p) (local.get $sz)))) (local.get $sz))
                        (call $convLoad (local.get $k) (local.get $c) (local.get $b) (local.get $p) (local.get $m))
                        (call $convMultiply (i32.const {{$diff}})
                            (i32.add (local.get $xs) (i32.mul (i32.rem_u (i32.sub (i32.add (local.get $qq) (local.get $parts)) (local.get $p)) (local.get $parts)) (local.get $sz)))
                            (local.get $m) (i32.const {{$old}}) (local.get $b))
{{- if .ConvFollow}}
                        (br_if $scan (i32.and
                            (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (i32.load offset={{.ConvFollowAt}} (local.get $k)))
                            (i32.lt_u (local.get $i) (local.get $parts))))
                    end
{{- end}}
{{- if and .ConvFade .ConvNoFade}}
                    (call $convBack (i32.const {{$diff}}) (i32.add (local.get $q) (i32.const 65536)) (local.get $at) (local.get $b)
                        (select (f32.const 0) (local.get $scale) (i32.load offset={{.ConvFadeAt}} (local.get $k)))
                        (select (f32.div (local.get $scale) (f32.convert_i32_u (local.get $b))) (f32.const 0) (i32.load offset={{.ConvFadeAt}} (local.get $k))))
{{- else if .ConvFade}}
                    (call $convBack (i32.const {{$diff}}) (i32.add (local.get $q) (i32.const 65536)) (local.get $at) (local.get $b) (f32.const 0) (f32.div (local.get $scale) (f32.convert_i32_u (local.get $b))))
{{- else}}
                    (call $convBack (i32.const {{$diff}}) (i32.add (local.get $q) (i32.const 65536)) (local.get $at) (local.get $b) (local.get $scale) (f32.const 0))
{{- end}}
{{- end}}
                ))
                (local.set $lp (i32.add (local.get $xs) (i32.mul (local.get $parts) (local.get $sz))))
            ))
            (local.set $b (i32.shl (local.get $b) (i32.const 3)))
            (br_if $levels (i32.lt_u (local.tee $l (i32.add (local.get $l) (i32.const 4))) (i32.const 12)))
        end
{{- end}}
{{- if .ConvScan}}
        ;; the head follows the buffer
        (call $convRead (local.get $k) (local.get $c) (i32.const 0) (i32.const 64) (i32.add (local.get $q) (i32.const 131072)) (i32.const 4))
{{- else if .ConvLoad}}
        (if (i32.eqz (local.get $n)) (then
            (call $convRead (local.get $k) (local.get $c) (i32.const 0) (i32.const 64) (i32.add (local.get $q) (i32.const 131072)) (i32.const 4))
        ))
{{- end}}
        ;; the input into its ring, and the head: the sum of the last 64
        ;; samples times the first 64 frames of the response
        (f32.store (i32.add (local.get $q) (i32.shl (i32.and (local.get $n) (i32.const 16383)) (i32.const 2)))
            (local.tee $x (f32.load (local.tee $m (i32.add (global.get $sp) (i32.shl (local.get $c) (i32.const 2)))))))
        (local.set $sum (f32.const 0))
        (local.set $i (i32.const 0))
        loop $head
            (local.set $sum (f32.add (local.get $sum) (f32.mul
                (f32.load offset=131072 (i32.add (local.get $q) (i32.shl (local.get $i) (i32.const 2))))
                (f32.load (i32.add (local.get $q) (i32.shl (i32.and (i32.sub (local.get $n) (local.get $i)) (i32.const 16383)) (i32.const 2)))))))
            (br_if $head (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (i32.const 64)))
        end
        (f32.store offset=65536 (local.tee $p (i32.add (local.get $q) (i32.shl (i32.and
                {{if .ConvPredelay}}(i32.add (local.get $n) (i32.load offset={{.ConvPredelayAt}} (local.get $k))){{else}}(local.get $n){{end}}
                (i32.const 16383)) (i32.const 2))))
            (f32.add (f32.load offset=65536 (local.get $p)) (local.get $sum)))
        ;; the output of this sample
        (local.set $y (f32.load offset=65536 (local.tee $p (i32.add (local.get $q) (i32.shl (i32.and (local.get $n) (i32.const 16383)) (i32.const 2))))))
{{- if .ConvGain}}
        (local.set $y (f32.mul (local.get $y) (local.get $gain)))
{{- end}}
        (f32.store offset=65536 (local.get $p) (f32.const 0))
{{- if .ConvDry}}
        (local.set $y (f32.add (local.get $y) (select
            (f32.mul (local.get $x) (call $input (i32.const {{.InputNumber "convolution" "dry"}})))
            (f32.const 0)
            (f32.ne (call $input (i32.const {{.InputNumber "convolution" "dry"}})) (f32.const 0)))))
{{- end}}
        (f32.store (local.get $m) (local.get $y))
        (local.set $q (i32.add (local.get $q) (i32.load offset=12 (local.get $k))))
{{- if .Stereo "convolution"}}
        (br_if $channels (i32.le_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (local.get $stereo)))
{{- end}}
    end
    (i32.store (local.get $s) (i32.add (local.get $n) (i32.const 1)))
    (global.set $convWRK (local.get $q))
)
{{- end}}