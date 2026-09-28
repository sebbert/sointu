{{- if .SpectralTable}}
;;-------------------------------------------------------------------------------
;;   Spectral units: spfft analyses a signal into a spectrum every size/4
;;   samples, other units change the spectrum, and spifft resynthesizes it with
;;   overlap-add. They run only in the first voice of their instrument. Matches
;;   vm/spectral.go operation by operation.
;;
;;   $spectralUnit returns the address of the unit's entry in
;;   su_spectral_table: the offset of the voice running it from su_voices, the
;;   offset of its state in su_spectral, and the offsets of its spectrum and
;;   source spectrum in su_spectrum_table. The state is the position in its
;;   ring and the count of the spectrum it processed last, followed by the
;;   ring. A spectrum entry is the offset of its data in su_spectral, the base
;;   2 logarithm of its size and the number of spectra written to it.
;;-------------------------------------------------------------------------------
(func $spectralUnit (result i32)
    (i32.add (i32.const {{index .Labels "su_spectral_table"}}) (i32.shl (call $scanOperand) (i32.const 4)))
)

;; $spectralVoice is true if the current voice runs the unit
(func $spectralVoice (param $u i32) (result i32)
    (i32.eq (i32.sub (global.get $voice) (i32.const {{index .Labels "su_voices"}})) (i32.load (local.get $u)))
)

;; $spectralInit computes the tables at su_spectral + SpectralHann, like
;; spectralTables in vm/spectral.go: the Hann window of the largest spectrum
;; size, and the twiddle factors of the FFT stages, the stage combining
;; transforms of size half having half factors e^(-πik/half) at half-1+k, as
;; wr, wr and -wi, wi, for SIMD complex multiplication.
(func $spectralInit (local $j i32) (local $half i32) (local $k i32) (local $a f32) (local $s f32) (local $p i32)
    loop $window
        (local.set $s (call $sin (f32.div (f32.mul (f32.convert_i32_u (local.get $j)) (f32.const 3.1415927)) (f32.const {{.SpectralMaxSize}}))))
        (f32.store offset={{add (index .Labels "su_spectral") .SpectralHann}} (i32.shl (local.get $j) (i32.const 2)) (f32.mul (local.get $s) (local.get $s)))
        (br_if $window (i32.lt_u (local.tee $j (i32.add (local.get $j) (i32.const 1))) (i32.const {{.SpectralMaxSize}})))
    end
    (local.set $half (i32.const 1))
    loop $stages
        (local.set $k (i32.const 0))
        loop $twiddles
            (local.set $a (f32.div (f32.mul (f32.convert_i32_u (local.get $k)) (f32.const -3.1415927)) (f32.convert_i32_u (local.get $half))))
            (local.set $p (i32.shl (i32.add (i32.sub (local.get $half) (i32.const 1)) (local.get $k)) (i32.const 3)))
            (local.set $s (call $sin (f32.add (local.get $a) (f32.const 1.5707964))))
            (f32.store offset={{add (index .Labels "su_spectral") .SpectralTwiddles}} (local.get $p) (local.get $s))
            (f32.store offset={{add (index .Labels "su_spectral") .SpectralTwiddles 4}} (local.get $p) (local.get $s))
            (local.set $s (call $sin (local.get $a)))
            (f32.store offset={{add (index .Labels "su_spectral") .SpectralTwiddles .SpectralTwiddleBytes}} (local.get $p) (f32.neg (local.get $s)))
            (f32.store offset={{add (index .Labels "su_spectral") .SpectralTwiddles .SpectralTwiddleBytes 4}} (local.get $p) (local.get $s))
            (br_if $twiddles (i32.lt_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (br_if $stages (i32.lt_u (local.tee $half (i32.shl (local.get $half) (i32.const 1))) (i32.const {{.SpectralMaxSize}})))
    end
)

;; $minU returns the smaller of two unsigned integers
(func $minU (param $a i32) (param $b i32) (result i32)
    (select (local.get $a) (local.get $b) (i32.lt_u (local.get $a) (local.get $b)))
)

;; $channelData returns the address of channel $c of the spectrum whose entry
;; is at $h: a spectrum has 2^log2 complex values for each channel
(func $channelData (param $h i32) (param $c i32) (result i32)
    (i32.add
        (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load (local.get $h)))
        (i32.shl (local.get $c) (i32.add (i32.load offset=4 (local.get $h)) (i32.const 3))))
)

;; $hann returns the Hann window of size 2^$log2n at $j: sin²(πj/n)
(func $hann (param $j i32) (param $log2n i32) (result f32)
    (f32.load offset={{add (index .Labels "su_spectral") .SpectralHann}}
        (i32.shl (local.get $j) (i32.sub (i32.const {{add .SpectralMaxLog2 2}}) (local.get $log2n))))
)

;; $fft transforms $n complex values at $x, interleaved, in place: X[k] is the
;; sum of x[j]·e^(-2πijk/n). $n is a power of 2. From the stage of size 2 on,
;; a v128 holds two complex values, which have neighbouring twiddle factors;
;; the lanes do the same operations as the scalar version in vm/spectral.go,
;; so the results are the same.
(func $fft (param $x i32) (param $n i32) (local $i i32) (local $j i32) (local $bit i32) (local $half i32) (local $k i32) (local $pi i32) (local $pj i32) (local $t i64) (local $w i32) (local $xi v128) (local $xj v128) (local $v v128)
    ;; bit reversal permutation
    loop $reverse
        (if (i32.lt_u (local.get $i) (local.get $j)) (then
            (local.set $pi (i32.add (local.get $x) (i32.shl (local.get $i) (i32.const 3))))
            (local.set $pj (i32.add (local.get $x) (i32.shl (local.get $j) (i32.const 3))))
            (local.set $t (i64.load (local.get $pi)))
            (i64.store (local.get $pi) (i64.load (local.get $pj)))
            (i64.store (local.get $pj) (local.get $t))
        ))
        (local.set $bit (i32.shr_u (local.get $n) (i32.const 1)))
        block $carried
            loop $carry
                (br_if $carried (i32.eqz (i32.and (local.get $j) (local.get $bit))))
                (local.set $j (i32.xor (local.get $j) (local.get $bit)))
                (local.set $bit (i32.shr_u (local.get $bit) (i32.const 1)))
                br $carry
            end
        end
        (local.set $j (i32.or (local.get $j) (local.get $bit)))
        (br_if $reverse (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (local.get $n)))
    end
    (if (i32.lt_u (local.get $n) (i32.const 2)) (then
        return
    ))
    ;; the stage of size 1 has the twiddle factor 1: a sum and a difference
    (local.set $i (i32.const 0))
    loop $pairs
        (local.set $pi (i32.add (local.get $x) (i32.shl (local.get $i) (i32.const 3))))
        (local.set $xi (i8x16.shuffle 0 1 2 3 4 5 6 7 0 1 2 3 4 5 6 7 (v128.load (local.get $pi)) (v128.load (local.get $pi))))
        (local.set $xj (i8x16.shuffle 8 9 10 11 12 13 14 15 8 9 10 11 12 13 14 15 (v128.load (local.get $pi)) (v128.load (local.get $pi))))
        (v128.store (local.get $pi) (i8x16.shuffle 0 1 2 3 4 5 6 7 16 17 18 19 20 21 22 23
            (f32x4.add (local.get $xi) (local.get $xj))
            (f32x4.sub (local.get $xi) (local.get $xj))))
        (br_if $pairs (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 2))) (local.get $n)))
    end
    (local.set $half (i32.const 2))
    block $stagesDone
        loop $stages
            (br_if $stagesDone (i32.ge_u (local.get $half) (local.get $n)))
            (local.set $i (i32.const 0))
            loop $blocks
                (local.set $k (i32.const 0))
                loop $butterflies
                    (local.set $pi (i32.add (local.get $x) (i32.shl (i32.add (local.get $i) (local.get $k)) (i32.const 3))))
                    (local.set $pj (i32.add (local.get $pi) (i32.shl (local.get $half) (i32.const 3))))
                    (local.set $w (i32.shl (i32.add (i32.sub (local.get $half) (i32.const 1)) (local.get $k)) (i32.const 3)))
                    (local.set $xi (v128.load (local.get $pi)))
                    (local.set $xj (v128.load (local.get $pj)))
                    ;; t = (wr·xr - wi·xi, wr·xi + wi·xr) for both values
                    (local.set $v (f32x4.add
                        (f32x4.mul (v128.load offset={{add (index .Labels "su_spectral") .SpectralTwiddles}} (local.get $w)) (local.get $xj))
                        (f32x4.mul
                            (v128.load offset={{add (index .Labels "su_spectral") .SpectralTwiddles .SpectralTwiddleBytes}} (local.get $w))
                            (i8x16.shuffle 4 5 6 7 0 1 2 3 12 13 14 15 8 9 10 11 (local.get $xj) (local.get $xj)))))
                    (v128.store (local.get $pj) (f32x4.sub (local.get $xi) (local.get $v)))
                    (v128.store (local.get $pi) (f32x4.add (local.get $xi) (local.get $v)))
                    (br_if $butterflies (i32.lt_u (local.tee $k (i32.add (local.get $k) (i32.const 2))) (local.get $half)))
                end
                (br_if $blocks (i32.lt_u
                    (local.tee $i (i32.add (local.get $i) (i32.shl (local.get $half) (i32.const 1))))
                    (local.get $n)))
            end
            (local.set $half (i32.shl (local.get $half) (i32.const 1)))
            br $stages
        end
    end
)
{{- end}}

{{- if .HasOp "spfft"}}
;;-------------------------------------------------------------------------------
;;   SPFFT opcode: pops a signal, or left and right, into the rings of the
;;   unit, and every size/4 samples replaces the spectrum with the FFT of the
;;   last size samples, windowed.
;;-------------------------------------------------------------------------------
(func $su_op_spfft (param $stereo i32) (local $u i32) (local $st i32) (local $h i32) (local $x i32) (local $n i32) (local $pos i32) (local $j i32) (local $c i32) (local $ring i32) (local $l f32) (local $r f32)
    (local.set $l (call $pop))
{{- if .Stereo "spfft"}}
    (if (local.get $stereo) (then
        (local.set $r (call $pop))
    ))
{{- end}}
    (local.set $u (call $spectralUnit))
    (if (i32.eqz (call $spectralVoice (local.get $u))) (then
        return
    ))
    ;; the ring of channel c is at st+16+c*4n
    (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
    (local.set $h (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=8 (local.get $u))))
    (local.set $n (i32.shl (i32.const 1) (i32.load offset=4 (local.get $h))))
    (local.set $pos (i32.load (local.get $st)))
    (local.set $ring (i32.add (local.get $st) (i32.shl (local.get $pos) (i32.const 2))))
    (f32.store offset=16 (local.get $ring) (local.get $l))
{{- if .Stereo "spfft"}}
    (if (local.get $stereo) (then
        (f32.store offset=16 (i32.add (local.get $ring) (i32.shl (local.get $n) (i32.const 2))) (local.get $r))
    ))
{{- end}}
    (i32.store (local.get $st) (local.tee $pos (i32.and (i32.add (local.get $pos) (i32.const 1)) (i32.sub (local.get $n) (i32.const 1)))))
    (if (i32.and (local.get $pos) (i32.sub (i32.shr_u (local.get $n) (i32.const 2)) (i32.const 1))) (then
        return
    ))
    loop $channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $ring (i32.add (local.get $st) (i32.mul (local.get $c) (i32.shl (local.get $n) (i32.const 2)))))
        (local.set $j (i32.const 0))
        loop $window
            (f32.store (i32.add (local.get $x) (i32.shl (local.get $j) (i32.const 3))) (f32.mul
                (f32.load offset=16 (i32.add (local.get $ring) (i32.shl
                    (i32.and (i32.add (local.get $pos) (local.get $j)) (i32.sub (local.get $n) (i32.const 1)))
                    (i32.const 2))))
                (call $hann (local.get $j) (i32.load offset=4 (local.get $h)))
            ))
            (f32.store offset=4 (i32.add (local.get $x) (i32.shl (local.get $j) (i32.const 3))) (f32.const 0))
            (br_if $window (i32.lt_u (local.tee $j (i32.add (local.get $j) (i32.const 1))) (local.get $n)))
        end
        (call $fft (local.get $x) (local.get $n))
        ;; the channels of both the unit and the spectrum
        (br_if $channels (i32.and
            (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h)))
            (i32.le_u (local.get $c) (local.get $stereo))))
    end
    (i32.store offset=8 (local.get $h) (i32.add (i32.load offset=8 (local.get $h)) (i32.const 1)))
)
{{- end}}

{{- if .HasOp "spifft"}}
;;-------------------------------------------------------------------------------
;;   SPIFFT opcode: overlap-adds each new spectrum, transformed back and
;;   windowed, to the rings of the unit, and pushes the next sample of each
;;   channel times gain. The inverse FFT of a spectrum with conjugate symmetry
;;   is real: real(ifft(X)) = real(fft(conj(X)))/n, where the bins above n/2
;;   mirror the bins below it. A mono unit averages the channels of a stereo
;;   spectrum; a stereo unit repeats a mono one.
;;-------------------------------------------------------------------------------
(func $su_op_spifft (param $stereo i32) (local $u i32) (local $st i32) (local $h i32) (local $x i32) (local $s i32) (local $n i32) (local $pos i32) (local $k i32) (local $p i32) (local $scale f32) (local $c i32) (local $rings i32) (local $ring i32) (local $l f32) (local $r f32)
    (local.set $u (call $spectralUnit))
    (if (call $spectralVoice (local.get $u)) (then
        (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
        (local.set $h (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=8 (local.get $u))))
        (local.set $n (i32.shl (i32.const 1) (i32.load offset=4 (local.get $h))))
        (local.set $pos (i32.load (local.get $st)))
        ;; the rings used: min(channels of the unit, channels of the spectrum)
        (local.set $rings (select (i32.const 2) (i32.const 1) (i32.and (local.get $stereo) (i32.eq (i32.load offset=12 (local.get $h)) (i32.const 2)))))
        (if (i32.ne (i32.load offset=8 (local.get $h)) (i32.load offset=4 (local.get $st))) (then
            (i32.store offset=4 (local.get $st) (i32.load offset=8 (local.get $h)))
            ;; the squared Hann windows overlapping by 3/4 sum to 3/2
            (local.set $scale (f32.div (f32.const 0.6666667) (f32.convert_i32_u (local.get $n))))
            (if (i32.gt_u (i32.load offset=12 (local.get $h)) (local.get $rings)) (then
                (local.set $scale (f32.mul (local.get $scale) (f32.const 0.5)))
            ))
            (local.set $s (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}}))
            loop $channels
                (local.set $x (call $channelData (local.get $h) (local.get $c)))
                (local.set $k (i32.const 0))
                loop $conjugate
                    (local.set $p (i32.shl (local.get $k) (i32.const 3)))
                    (f32.store (i32.add (local.get $s) (local.get $p)) (f32.load (i32.add (local.get $x) (local.get $p))))
                    (f32.store offset=4 (i32.add (local.get $s) (local.get $p)) (f32.neg (f32.load offset=4 (i32.add (local.get $x) (local.get $p)))))
                    (br_if $conjugate (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.shr_u (local.get $n) (i32.const 1))))
                end
                loop $mirror
                    (i64.store (i32.add (local.get $s) (i32.shl (local.get $k) (i32.const 3)))
                        (i64.load (i32.add (local.get $x) (i32.shl (i32.sub (local.get $n) (local.get $k)) (i32.const 3)))))
                    (br_if $mirror (i32.lt_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $n)))
                end
                (call $fft (local.get $s) (local.get $n))
                (local.set $ring (call $spifftRing (local.get $st) (local.get $n) (local.get $c) (local.get $rings)))
                (local.set $k (i32.const 0))
                loop $overlapAdd
                    (local.set $p (i32.add (local.get $ring) (i32.shl
                        (i32.and (i32.add (local.get $pos) (local.get $k)) (i32.sub (local.get $n) (i32.const 1)))
                        (i32.const 2))))
                    (f32.store (local.get $p) (f32.add
                        (f32.load (local.get $p))
                        (f32.mul
                            (f32.mul (f32.load (i32.add (local.get $s) (i32.shl (local.get $k) (i32.const 3)))) (local.get $scale))
                            (call $hann (local.get $k) (i32.load offset=4 (local.get $h))))
                    ))
                    (br_if $overlapAdd (i32.lt_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $n)))
                end
                (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
            end
        ))
        (local.set $p (i32.add (call $spifftRing (local.get $st) (local.get $n) (i32.const 0) (local.get $rings)) (i32.shl (local.get $pos) (i32.const 2))))
        (local.set $l (f32.mul (f32.load (local.get $p)) (call $input (i32.const {{.InputNumber "spifft" "gain"}}))))
        (f32.store (local.get $p) (f32.const 0))
        (local.set $p (i32.add (call $spifftRing (local.get $st) (local.get $n) (i32.const 1) (local.get $rings)) (i32.shl (local.get $pos) (i32.const 2))))
        (local.set $r (f32.mul (f32.load (local.get $p)) (call $input (i32.const {{.InputNumber "spifft" "gain"}}))))
        (if (i32.eq (local.get $rings) (i32.const 1)) (then
            (local.set $r (local.get $l))
        ))
        (f32.store (local.get $p) (f32.const 0))
        (i32.store (local.get $st) (i32.and (i32.add (local.get $pos) (i32.const 1)) (i32.sub (local.get $n) (i32.const 1))))
    ))
{{- if .Stereo "spifft"}}
    (if (local.get $stereo) (then
        (call $push (local.get $r))
    ))
{{- end}}
    (call $push (local.get $l))
)

;; $spifftRing returns the address of the ring of an spifft unit, whose state
;; is at $st, for channel $c of its spectrum: ring min(c, rings-1), c and
;; rings-1 being 0 or 1
(func $spifftRing (param $st i32) (param $n i32) (param $c i32) (param $rings i32) (result i32)
    (i32.add
        (i32.add (local.get $st) (i32.const 16))
        (select (i32.shl (local.get $n) (i32.const 2)) (i32.const 0) (i32.and (local.get $c) (i32.sub (local.get $rings) (i32.const 1)))))
)
{{- end}}

{{- if .HasOp "spcopy"}}
;;-------------------------------------------------------------------------------
;;   SPCOPY opcode: copies each new spectrum of the source to its spectrum
;;-------------------------------------------------------------------------------
(func $su_op_spcopy (param $stereo i32) (local $u i32) (local $st i32) (local $h i32) (local $src i32)
    (local.set $u (call $spectralUnit))
    (if (i32.eqz (call $spectralVoice (local.get $u))) (then
        return
    ))
    (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
    (local.set $h (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=8 (local.get $u))))
    (local.set $src (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=12 (local.get $u))))
    (if (i32.eq (i32.load offset=8 (local.get $src)) (i32.load offset=4 (local.get $st))) (then
        return
    ))
    (i32.store offset=4 (local.get $st) (i32.load offset=8 (local.get $src)))
    (memory.copy
        (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load (local.get $h)))
        (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load (local.get $src)))
        ;; the smaller of the two, channels * 8 bytes * size
        (call $minU
            (i32.shl (i32.load offset=12 (local.get $h)) (i32.add (i32.load offset=4 (local.get $h)) (i32.const 3)))
            (i32.shl (i32.load offset=12 (local.get $src)) (i32.add (i32.load offset=4 (local.get $src)) (i32.const 3))))
    )
    (i32.store offset=8 (local.get $h) (i32.add (i32.load offset=8 (local.get $h)) (i32.const 1)))
)
{{- end}}
{{- if or (.HasOp "spfilter") (.HasOp "spcompress") (.HasOp "spblur") (.HasOp "spgate") (.HasOp "spphase") (.HasOp "spscale") (.HasOp "spformant") (.HasOp "spcross") (.HasOp "spcomb")}}
;; $spectralFrame returns the address of the spectrum entry of a modifying
;; spectral unit if the current voice runs it and there is a new spectrum it
;; has not processed yet, marking it processed; otherwise 0.
(func $spectralFrame (param $u i32) (result i32) (local $st i32) (local $h i32)
    (if (i32.eqz (call $spectralVoice (local.get $u))) (then
        (return (i32.const 0))
    ))
    (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
    (local.set $h (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=8 (local.get $u))))
    (if (i32.eq (i32.load offset=8 (local.get $h)) (i32.load offset=4 (local.get $st))) (then
        (return (i32.const 0))
    ))
    (i32.store offset=4 (local.get $st) (i32.load offset=8 (local.get $h)))
    (local.get $h)
)
{{- end}}

{{- if or (.HasOp "spblur") (.HasOp "spphase")}}
;; $tablePhase returns the cosine and sine of the phase -πp/128, p modulo 256,
;; from the twiddle factors e^(-πik/128), k < 128
(func $tablePhase (param $p i32) (result f32 f32) (local $j i32)
    (local.set $j (i32.shl (i32.add (i32.const 127) (i32.and (local.get $p) (i32.const 127))) (i32.const 3)))
    (f32.load offset={{add (index .Labels "su_spectral") .SpectralTwiddles}} (local.get $j))
    (f32.load offset={{add (index .Labels "su_spectral") .SpectralTwiddles .SpectralTwiddleBytes 4}} (local.get $j))
    (if (param f32 f32) (result f32 f32) (i32.and (local.get $p) (i32.const 128)) (then
        (local.set $j (i32.reinterpret_f32 (f32.neg)))
        f32.neg
        (f32.reinterpret_i32 (local.get $j))
    ))
)

;; $randomPhase updates the random number generator at $rng and returns the
;; cosine and sine of a random phase, one of 256
(func $randomPhase (param $rng i32) (result f32 f32) (local $r i32)
    (i32.store (local.get $rng) (local.tee $r (i32.add (i32.mul (i32.load (local.get $rng)) (i32.const 1664525)) (i32.const 1013904223))))
    (call $tablePhase (i32.shr_u (local.get $r) (i32.const 24)))
)
{{- end}}

{{- if .HasOp "spfilter"}}
;;-------------------------------------------------------------------------------
;;   SPFILTER opcode: removes the bins below low and above high, and tilts the
;;   rest by frequency^e, e = tilt*4-2, around 1 kHz
;;-------------------------------------------------------------------------------
(func $su_op_spfilter (param $stereo i32) (local $c i32) (local $h i32) (local $x i32) (local $n i32) (local $k i32) (local $lo f32) (local $hi f32) (local $e f32) (local $ref f32) (local $fk f32) (local $g f32)
    (local.set $h (call $spectralFrame (call $spectralUnit)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $n (i32.shl (i32.const 1) (i32.load offset=4 (local.get $h))))
    (local.set $lo (f32.mul (f32.convert_i32_u (i32.shr_u (local.get $n) (i32.const 1)))
        (f32.div (f32.sub (call $pow (f32.const 2) (f32.mul (call $input (i32.const {{.InputNumber "spfilter" "low"}})) (f32.const 10))) (f32.const 1)) (f32.const 1023))))
    (local.set $hi (f32.mul (f32.convert_i32_u (i32.shr_u (local.get $n) (i32.const 1)))
        (f32.div (f32.sub (call $pow (f32.const 2) (f32.mul (call $input (i32.const {{.InputNumber "spfilter" "high"}})) (f32.const 10))) (f32.const 1)) (f32.const 1023))))
    (local.set $e (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "spfilter" "tilt"}})) (f32.const 4)) (f32.const 2)))
    (local.set $ref (f32.div (f32.convert_i32_u (local.get $n)) (f32.const 44.1)))
    loop $channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $k (i32.const 0))
        loop $bins
            (local.set $fk (f32.convert_i32_u (local.get $k)))
            (if (i32.or (f32.lt (local.get $fk) (local.get $lo)) (f32.gt (local.get $fk) (local.get $hi))) (then
                (i64.store (local.get $x) (i64.const 0))
            )(else
                (if (f32.ne (local.get $e) (f32.const 0)) (then
                    (local.set $g (call $pow (f32.div (f32.max (local.get $fk) (f32.const 1)) (local.get $ref)) (local.get $e)))
                    (f32.store (local.get $x) (f32.mul (f32.load (local.get $x)) (local.get $g)))
                    (f32.store offset=4 (local.get $x) (f32.mul (f32.load offset=4 (local.get $x)) (local.get $g)))
                ))
            ))
            (local.set $x (i32.add (local.get $x) (i32.const 8)))
            (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (i32.shr_u (local.get $n) (i32.const 1))))
        end
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)
{{- end}}

{{- if .HasOp "spcompress"}}
;;-------------------------------------------------------------------------------
;;   SPCOMPRESS opcode: scales each bin by (mean/envelope)^amount, where the
;;   envelope is the mean magnitude of the bins within width
;;-------------------------------------------------------------------------------
(func $su_op_spcompress (param $stereo i32) (local $c i32) (local $h i32) (local $x i32) (local $half i32) (local $k i32) (local $w i32) (local $lo i32) (local $hi i32) (local $a f32) (local $sum f32) (local $mean f32) (local $g f32) (local $p i32)
    (local.set $h (call $spectralFrame (call $spectralUnit)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $a (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "spcompress" "amount"}})) (f32.const 2)) (f32.const 1)))
    (if (f32.eq (local.get $a) (f32.const 0)) (then
        return
    ))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    (local.set $w (i32.add (i32.const 1) (i32.trunc_f32_u (f32.mul
        (f32.min (f32.max (call $input (i32.const {{.InputNumber "spcompress" "width"}})) (f32.const 0)) (f32.const 1))
        (f32.convert_i32_u (i32.shr_u (local.get $half) (i32.const 4)))))))
    loop $channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $k (i32.const 0))
        (local.set $sum (f32.const 0))
        ;; prefix sums of the magnitudes in the scratch space
        (f32.store offset={{add (index .Labels "su_spectral") .SpectralScratch}} (i32.const 0) (f32.const 0))
        loop $sums
            (local.set $p (i32.add (local.get $x) (i32.shl (local.get $k) (i32.const 3))))
            (local.set $sum (f32.add (local.get $sum) (f32.sqrt (f32.add
                (f32.mul (f32.load (local.get $p)) (f32.load (local.get $p)))
                (f32.mul (f32.load offset=4 (local.get $p)) (f32.load offset=4 (local.get $p)))))))
            (f32.store offset={{add (index .Labels "su_spectral") .SpectralScratch 4}} (i32.shl (local.get $k) (i32.const 2)) (local.get $sum))
            (br_if $sums (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (local.set $mean (f32.div (local.get $sum) (f32.convert_i32_u (i32.add (local.get $half) (i32.const 1)))))
        (local.set $k (i32.const 0))
        loop $bins
            (local.set $lo (select (i32.sub (local.get $k) (local.get $w)) (i32.const 0) (i32.gt_s (i32.sub (local.get $k) (local.get $w)) (i32.const 0))))
            (local.set $hi (select (i32.add (local.get $k) (local.get $w)) (local.get $half) (i32.lt_s (i32.add (local.get $k) (local.get $w)) (local.get $half))))
            (local.set $g (call $pow
                (f32.div
                    (f32.add (local.get $mean) (f32.const 1e-9))
                    (f32.add
                        (f32.div
                            (f32.sub
                                (f32.load offset={{add (index .Labels "su_spectral") .SpectralScratch 4}} (i32.shl (local.get $hi) (i32.const 2)))
                                (f32.load offset={{add (index .Labels "su_spectral") .SpectralScratch}} (i32.shl (local.get $lo) (i32.const 2))))
                            (f32.convert_i32_s (i32.sub (i32.add (local.get $hi) (i32.const 1)) (local.get $lo))))
                        (f32.const 1e-9)))
                (local.get $a)))
            (local.set $p (i32.add (local.get $x) (i32.shl (local.get $k) (i32.const 3))))
            (f32.store (local.get $p) (f32.mul (f32.load (local.get $p)) (local.get $g)))
            (f32.store offset=4 (local.get $p) (f32.mul (f32.load offset=4 (local.get $p)) (local.get $g)))
            (br_if $bins (i32.le_s (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)
{{- end}}

{{- if .HasOp "spblur"}}
;;-------------------------------------------------------------------------------
;;   SPBLUR opcode: smooths the magnitudes over time, keeping the phases, or
;;   while frozen holds the magnitudes with random phases
;;-------------------------------------------------------------------------------
(func $su_op_spblur (param $stereo i32) (local $c i32) (local $u i32) (local $h i32) (local $st i32) (local $x i32) (local $y i32) (local $half i32) (local $k i32) (local $a f32) (local $frozen i32) (local $xr f32) (local $xi f32) (local $yr f32) (local $yi f32) (local $m f32) (local $b f32) (local $t f32)
    (local.set $u (call $spectralUnit))
    (local.set $h (call $spectralFrame (local.get $u)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $a (call $input (i32.const {{.InputNumber "spblur" "amount"}})))
    (local.set $frozen (f32.gt (call $input (i32.const {{.InputNumber "spblur" "freeze"}})) (f32.const 0.5)))
    (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    loop $channels
        ;; the held spectrum of channel c is at st+16+c*8(half+1)
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $y (i32.add (i32.add (local.get $st) (i32.const 16)) (i32.mul (local.get $c) (i32.shl (i32.add (local.get $half) (i32.const 1)) (i32.const 3)))))
        (local.set $k (i32.const 0))
        loop $bins
            (local.set $yr (f32.load (local.get $y)))
            (local.set $yi (f32.load offset=4 (local.get $y)))
            (if (local.get $frozen) (then
                (local.set $m (f32.sqrt (f32.add (f32.mul (local.get $yr) (local.get $yr)) (f32.mul (local.get $yi) (local.get $yi)))))
                (call $randomPhase (i32.add (local.get $st) (i32.const 8)))
                (local.set $t)
                (local.set $b)
                (f32.store (local.get $x) (f32.mul (local.get $m) (local.get $b)))
                (f32.store offset=4 (local.get $x) (f32.mul (local.get $m) (local.get $t)))
            )(else
                (local.set $xr (f32.load (local.get $x)))
                (local.set $xi (f32.load offset=4 (local.get $x)))
                (local.set $m (f32.sqrt (f32.add (f32.mul (local.get $xr) (local.get $xr)) (f32.mul (local.get $xi) (local.get $xi)))))
                (local.set $b (f32.add
                    (f32.mul
                        (f32.sub (f32.sqrt (f32.add (f32.mul (local.get $yr) (local.get $yr)) (f32.mul (local.get $yi) (local.get $yi)))) (local.get $m))
                        (local.get $a))
                    (local.get $m)))
                (if (f32.gt (local.get $m) (f32.const 0)) (then
                    (local.set $t (f32.div (local.get $b) (local.get $m)))
                    (local.set $yr (f32.mul (local.get $xr) (local.get $t)))
                    (local.set $yi (f32.mul (local.get $xi) (local.get $t)))
                )(else
                    (local.set $yr (local.get $b))
                    (local.set $yi (f32.const 0))
                ))
                (f32.store (local.get $y) (local.get $yr))
                (f32.store offset=4 (local.get $y) (local.get $yi))
                (f32.store (local.get $x) (local.get $yr))
                (f32.store offset=4 (local.get $x) (local.get $yi))
            ))
            (local.set $x (i32.add (local.get $x) (i32.const 8)))
            (local.set $y (i32.add (local.get $y) (i32.const 8)))
            (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)
{{- end}}

{{- if .HasOp "spgate"}}
;;-------------------------------------------------------------------------------
;;   SPGATE opcode: removes the bins quieter than the threshold, from -96 to 0
;;   dB of a full scale sine, whose magnitude is n/4; or the louder ones
;;-------------------------------------------------------------------------------
(func $su_op_spgate (param $stereo i32) (local $c i32) (local $h i32) (local $invert i32) (local $x i32) (local $half i32) (local $k i32) (local $thr f32)
    (local.set $h (call $spectralFrame (call $spectralUnit)))
    (local.set $invert (call $scanOperand))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    (local.set $thr (f32.mul
        (call $pow (f32.const 2) (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "spgate" "threshold"}})) (f32.const 16)) (f32.const 16)))
        (f32.convert_i32_u (i32.shr_u (local.get $half) (i32.const 1)))))
    (local.set $thr (f32.mul (local.get $thr) (local.get $thr)))
    loop $channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $k (i32.const 0))
        loop $bins
            (if (i32.ne
                    (f32.lt (f32.add
                        (f32.mul (f32.load (local.get $x)) (f32.load (local.get $x)))
                        (f32.mul (f32.load offset=4 (local.get $x)) (f32.load offset=4 (local.get $x)))) (local.get $thr))
                    (local.get $invert)) (then
                (i64.store (local.get $x) (i64.const 0))
            ))
            (local.set $x (i32.add (local.get $x) (i32.const 8)))
            (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)
{{- end}}

{{- if .HasOp "spphase"}}
;;-------------------------------------------------------------------------------
;;   SPPHASE opcode: disperse rotates bin k by -πp/128, p growing with k²,
;;   random by a random phase, robot blends toward phase 0
;;-------------------------------------------------------------------------------
;; $rotate returns x times e^(iθ), given the cosine and sine of θ
(func $rotate (param $xr f32) (param $xi f32) (param $c f32) (param $s f32) (result f32 f32)
    (f32.sub (f32.mul (local.get $xr) (local.get $c)) (f32.mul (local.get $xi) (local.get $s)))
    (f32.add (f32.mul (local.get $xr) (local.get $s)) (f32.mul (local.get $xi) (local.get $c)))
)

(func $su_op_spphase (param $stereo i32) (local $c i32) (local $u i32) (local $h i32) (local $mode i32) (local $x i32) (local $half i32) (local $k i32) (local $a f32) (local $xr f32) (local $xi f32) (local $st i32) (local $m f32)
    (local.set $u (call $spectralUnit))
    (local.set $mode (call $scanOperand))
    (local.set $h (call $spectralFrame (local.get $u)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $a (call $input (i32.const {{.InputNumber "spphase" "amount"}})))
    (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    loop $channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $k (i32.const 0))
        loop $bins
            (local.set $xr (f32.load (local.get $x)))
            (local.set $xi (f32.load offset=4 (local.get $x)))
            (if (i32.eqz (local.get $mode)) (then ;; disperse
                (call $rotate (local.get $xr) (local.get $xi) (call $tablePhase (i32.trunc_sat_f32_s
                    (f32.div
                        (f32.mul (f32.mul (f32.mul (local.get $a) (f32.convert_i32_u (local.get $k))) (f32.convert_i32_u (local.get $k))) (f32.const 64))
                        (f32.convert_i32_u (local.get $half))))))
                (local.set $xi)
                (local.set $xr)
            )(else (if (i32.eq (local.get $mode) (i32.const 1)) (then ;; random
                (i32.store offset=8 (local.get $st) (i32.add (i32.mul (i32.load offset=8 (local.get $st)) (i32.const 1664525)) (i32.const 1013904223)))
                (call $rotate (local.get $xr) (local.get $xi) (call $tablePhase (i32.trunc_sat_f32_s
                    (f32.mul (local.get $a) (f32.convert_i32_u (i32.shr_u (i32.load offset=8 (local.get $st)) (i32.const 24)))))))
                (local.set $xi)
                (local.set $xr)
            )(else ;; robot
                (local.set $m (f32.sqrt (f32.add (f32.mul (local.get $xr) (local.get $xr)) (f32.mul (local.get $xi) (local.get $xi)))))
                (local.set $xr (f32.add (f32.mul (f32.sub (local.get $m) (local.get $xr)) (local.get $a)) (local.get $xr)))
                (local.set $xi (f32.sub (local.get $xi) (f32.mul (local.get $xi) (local.get $a))))
            ))))
            (f32.store (local.get $x) (local.get $xr))
            (f32.store offset=4 (local.get $x) (local.get $xi))
            (local.set $x (i32.add (local.get $x) (i32.const 8)))
            (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)
{{- end}}

{{- if .HasOp "spscale"}}
;;-------------------------------------------------------------------------------
;;   SPSCALE opcode: moves bin k to k*scale+shift, in the scratch space
;;-------------------------------------------------------------------------------
(func $su_op_spscale (param $stereo i32) (local $c i32) (local $h i32) (local $x i32) (local $half i32) (local $k i32) (local $i i32) (local $ratio f32) (local $offset f32) (local $j f32) (local $p i32) (local $q i32)
    (local.set $h (call $spectralFrame (call $spectralUnit)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    (local.set $ratio (call $pow (f32.const 2) (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "spscale" "scale"}})) (f32.const 2)) (f32.const 1))))
    (local.set $offset (f32.mul
        (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "spscale" "shift"}})) (f32.const 2)) (f32.const 1))
        (f32.mul (f32.convert_i32_u (i32.shl (local.get $half) (i32.const 1))) (f32.const 0.022675737))))
    loop $channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $k (i32.const 0))
        (memory.fill (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}}) (i32.const 0) (i32.shl (i32.add (local.get $half) (i32.const 1)) (i32.const 3)))
        loop $bins
            (local.set $j (f32.add (f32.mul (f32.convert_i32_u (local.get $k)) (local.get $ratio)) (local.get $offset)))
            (if (f32.ge (local.get $j) (f32.const 0)) (then
                (local.set $i (i32.trunc_sat_f32_u (f32.add (local.get $j) (f32.const 0.5))))
                (if (i32.le_u (local.get $i) (local.get $half)) (then
                    (local.set $p (i32.add (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}}) (i32.shl (local.get $i) (i32.const 3))))
                    (local.set $q (i32.add (local.get $x) (i32.shl (local.get $k) (i32.const 3))))
                    (f32.store (local.get $p) (f32.add (f32.load (local.get $p)) (f32.load (local.get $q))))
                    (f32.store offset=4 (local.get $p) (f32.add (f32.load offset=4 (local.get $p)) (f32.load offset=4 (local.get $q))))
                ))
            ))
            (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (memory.copy (local.get $x) (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}}) (i32.shl (i32.add (local.get $half) (i32.const 1)) (i32.const 3)))
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)
{{- end}}

{{- if or (.HasOp "spformant") (.HasOp "spcross")}}
;; $envelope writes the average magnitudes of the bins of the spectrum at $x
;; within $w bins to $env, using $sums for the prefix sums of the magnitudes
(func $envelope (param $x i32) (param $half i32) (param $w i32) (param $sums i32) (param $env i32) (local $k i32) (local $lo i32) (local $hi i32) (local $sum f32) (local $p i32)
    (f32.store (local.get $sums) (f32.const 0))
    loop $sums
        (local.set $p (i32.add (local.get $x) (i32.shl (local.get $k) (i32.const 3))))
        (local.set $sum (f32.add (local.get $sum) (f32.sqrt (f32.add
            (f32.mul (f32.load (local.get $p)) (f32.load (local.get $p)))
            (f32.mul (f32.load offset=4 (local.get $p)) (f32.load offset=4 (local.get $p)))))))
        (f32.store offset=4 (i32.add (local.get $sums) (i32.shl (local.get $k) (i32.const 2))) (local.get $sum))
        (br_if $sums (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
    end
    (local.set $k (i32.const 0))
    loop $envelope
        (local.set $lo (select (i32.sub (local.get $k) (local.get $w)) (i32.const 0) (i32.gt_s (i32.sub (local.get $k) (local.get $w)) (i32.const 0))))
        (local.set $hi (select (i32.add (local.get $k) (local.get $w)) (local.get $half) (i32.lt_s (i32.add (local.get $k) (local.get $w)) (local.get $half))))
        (f32.store (i32.add (local.get $env) (i32.shl (local.get $k) (i32.const 2)))
            (f32.div
                (f32.sub
                    (f32.load offset=4 (i32.add (local.get $sums) (i32.shl (local.get $hi) (i32.const 2))))
                    (f32.load (i32.add (local.get $sums) (i32.shl (local.get $lo) (i32.const 2)))))
                (f32.convert_i32_s (i32.sub (i32.add (local.get $hi) (i32.const 1)) (local.get $lo)))))
        (br_if $envelope (i32.le_s (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
    end
)
{{- end}}

{{- if .HasOp "spformant"}}
;;-------------------------------------------------------------------------------
;;   SPFORMANT opcode: moves the envelope, the average magnitude within width,
;;   by scaling its frequencies. The prefix sums of the magnitudes are at the
;;   start of the scratch space, the envelope after the first half.
;;-------------------------------------------------------------------------------
(func $su_op_spformant (param $stereo i32) (local $c i32) (local $h i32) (local $x i32) (local $half i32) (local $k i32) (local $i i32) (local $ratio f32) (local $src f32) (local $e f32) (local $g f32) (local $p i32)
    (local.set $h (call $spectralFrame (call $spectralUnit)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    (local.set $ratio (call $pow (f32.const 2) (f32.sub (f32.mul (call $input (i32.const {{.InputNumber "spformant" "shift"}})) (f32.const 2)) (f32.const 1))))
    loop $channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $k (i32.const 0))
        (call $envelope (local.get $x) (local.get $half)
            (i32.add (i32.const 1) (i32.trunc_f32_u (f32.mul
                (f32.min (f32.max (call $input (i32.const {{.InputNumber "spformant" "width"}})) (f32.const 0)) (f32.const 1))
                (f32.convert_i32_u (i32.shr_u (local.get $half) (i32.const 4))))))
            (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}})
            (i32.add (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}}) (i32.shl (local.get $half) (i32.const 3))))
        loop $bins
            (local.set $src (f32.div (f32.convert_i32_u (local.get $k)) (local.get $ratio)))
            (local.set $i (i32.trunc_sat_f32_u (local.get $src)))
            (local.set $e (call $envelopeAt (local.get $half) (local.get $half)))
            (if (i32.lt_u (local.get $i) (local.get $half)) (then
                (local.set $e (f32.add
                    (f32.mul
                        (f32.sub (call $envelopeAt (local.get $half) (i32.add (local.get $i) (i32.const 1))) (call $envelopeAt (local.get $half) (local.get $i)))
                        (f32.sub (local.get $src) (f32.convert_i32_u (local.get $i))))
                    (call $envelopeAt (local.get $half) (local.get $i))))
            ))
            (local.set $g (f32.div (f32.add (local.get $e) (f32.const 1e-9)) (f32.add (call $envelopeAt (local.get $half) (local.get $k)) (f32.const 1e-9))))
            (local.set $p (i32.add (local.get $x) (i32.shl (local.get $k) (i32.const 3))))
            (f32.store (local.get $p) (f32.mul (f32.load (local.get $p)) (local.get $g)))
            (f32.store offset=4 (local.get $p) (f32.mul (f32.load offset=4 (local.get $p)) (local.get $g)))
            (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)

;; $envelopeAt returns the envelope of spformant at bin $k
(func $envelopeAt (param $half i32) (param $k i32) (result f32)
    (f32.load offset={{add (index .Labels "su_spectral") .SpectralScratch}} (i32.shl (i32.add (local.get $k) (i32.shl (local.get $half) (i32.const 1))) (i32.const 2)))
)
{{- end}}

{{- if .HasOp "spcross"}}
;;-------------------------------------------------------------------------------
;;   SPCROSS opcode: scales each bin by (source envelope/envelope)^amount. The
;;   scratch space has the prefix sums and envelope of the source, and from
;;   n+4 on, those of the spectrum.
;;-------------------------------------------------------------------------------
(func $su_op_spcross (param $stereo i32) (local $c i32) (local $u i32) (local $h i32) (local $src i32) (local $x i32) (local $half i32) (local $k i32) (local $w i32) (local $a f32) (local $g f32)
    (local.set $u (call $spectralUnit))
    (local.set $h (call $spectralFrame (local.get $u)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    (local.set $src (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=12 (local.get $u))))
    (if (i32.ne (i32.load offset=4 (local.get $src)) (i32.load offset=4 (local.get $h))) (then
        return
    ))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    (local.set $w (i32.trunc_f32_u (f32.mul
        (f32.min (f32.max (call $input (i32.const {{.InputNumber "spcross" "width"}})) (f32.const 0)) (f32.const 1))
        (f32.convert_i32_u (i32.shr_u (local.get $half) (i32.const 4))))))
    (local.set $a (call $input (i32.const {{.InputNumber "spcross" "amount"}})))
    loop $channels
        ;; a mono source is used for both channels
        (local.set $x (call $channelData (local.get $h) (local.get $c)))
        (local.set $k (i32.const 0))
        (call $envelope (call $channelData (local.get $src) (call $minU (local.get $c) (i32.sub (i32.load offset=12 (local.get $src)) (i32.const 1)))) (local.get $half) (local.get $w)
            (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}})
            (i32.add (i32.const {{add (index .Labels "su_spectral") .SpectralScratch 8}}) (i32.shl (local.get $half) (i32.const 2))))
        (call $envelope (local.get $x) (local.get $half) (local.get $w)
            (i32.add (i32.const {{add (index .Labels "su_spectral") .SpectralScratch 16}}) (i32.shl (local.get $half) (i32.const 3)))
            (i32.add (i32.const {{add (index .Labels "su_spectral") .SpectralScratch 24}}) (i32.mul (local.get $half) (i32.const 12))))
        loop $bins
            (local.set $g (call $pow
                (f32.div
                    (f32.add (f32.load offset={{add (index .Labels "su_spectral") .SpectralScratch 8}} (i32.shl (i32.add (local.get $half) (local.get $k)) (i32.const 2))) (f32.const 1e-9))
                    (f32.add (f32.load offset={{add (index .Labels "su_spectral") .SpectralScratch 24}} (i32.shl (i32.add (i32.mul (local.get $half) (i32.const 3)) (local.get $k)) (i32.const 2))) (f32.const 1e-9)))
                (local.get $a)))
            (f32.store (local.get $x) (f32.mul (f32.load (local.get $x)) (local.get $g)))
            (f32.store offset=4 (local.get $x) (f32.mul (f32.load offset=4 (local.get $x)) (local.get $g)))
            (local.set $x (i32.add (local.get $x) (i32.const 8)))
            (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
        end
        (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
    end
)
{{- end}}

{{- if .HasOp "spcomb"}}
;;-------------------------------------------------------------------------------
;;   SPCOMB opcode: keeps the bins near the harmonics of up to 8 notes: the
;;   notes held in the voices given by the operands, or if none, the note of
;;   its own voice and the intervals above it. The frequencies of the notes
;;   are in the scratch space.
;;-------------------------------------------------------------------------------
;; $noteFrequency returns the frequency of a note in Hz: 69 is A 440 Hz
(func $noteFrequency (param $note i32) (result f32)
    (f32.mul (f32.const 440) (call $pow (f32.const 2) (f32.div (f32.convert_i32_s (i32.sub (local.get $note) (i32.const 69))) (f32.const 12))))
)

;; $addNote adds the frequency of a note to the list of $nf notes, returning the
;; new count
(func $addNote (param $nf i32) (param $note i32) (result i32)
    (f32.store offset={{add (index .Labels "su_spectral") .SpectralScratch}} (i32.shl (local.get $nf) (i32.const 2)) (call $noteFrequency (local.get $note)))
    (i32.add (local.get $nf) (i32.const 1))
)

(func $su_op_spcomb (param $stereo i32) (local $c i32) (local $p i32) (local $u i32) (local $h i32) (local $v i32) (local $end i32) (local $nf i32) (local $note i32) (local $intervals i32) (local $i i32) (local $x i32) (local $half i32) (local $k i32) (local $j i32) (local $sharp f32) (local $binHz f32) (local $m f32) (local $r f32) (local $a f32) (local $g f32)
    (local.set $u (call $spectralUnit))
    (local.set $v (call $scanOperand))
    (local.set $end (i32.add (local.get $v) (call $scanOperand)))
    (local.set $intervals (global.get $VAL))
    (global.set $VAL (i32.add (global.get $VAL) (i32.const 3)))
    (local.set $h (call $spectralFrame (local.get $u)))
    (if (i32.eqz (local.get $h)) (then
        return
    ))
    block $gathered
        loop $voices
            (br_if $gathered (i32.ge_u (local.get $v) (local.get $end)))
            (br_if $gathered (i32.ge_u (local.get $nf) (i32.const 8)))
            (local.set $note (i32.load offset={{index .Labels "su_voices"}} (i32.shl (local.get $v) (i32.const 12))))
            (if (i32.load offset={{add (index .Labels "su_voices") 4}} (i32.shl (local.get $v) (i32.const 12))) (then
                (if (local.get $note) (then
                    (local.set $nf (call $addNote (local.get $nf) (local.get $note)))
                ))
            ))
            (local.set $v (i32.add (local.get $v) (i32.const 1)))
            br $voices
        end
    end
    (if (i32.eqz (local.get $nf)) (then
        (local.set $note (i32.load (global.get $voice)))
        (if (local.get $note) (then
            (local.set $nf (call $addNote (local.get $nf) (local.get $note)))
            loop $intervalLoop
                (if (i32.load8_u (i32.add (local.get $intervals) (local.get $i))) (then
                    (local.set $nf (call $addNote (local.get $nf) (i32.add (local.get $note) (i32.load8_u (i32.add (local.get $intervals) (local.get $i))))))
                ))
                (br_if $intervalLoop (i32.lt_u (local.tee $i (i32.add (local.get $i) (i32.const 1))) (i32.const 3)))
            end
        ))
    ))
    (if (i32.eqz (local.get $nf)) (then
        return
    ))
    (local.set $half (i32.shl (i32.const 1) (i32.sub (i32.load offset=4 (local.get $h)) (i32.const 1))))
    (local.set $sharp (f32.add (f32.mul (call $input (i32.const {{.InputNumber "spcomb" "q"}})) (f32.const 30)) (f32.const 2)))
    (local.set $binHz (f32.div (f32.const 44100) (f32.convert_i32_u (i32.shl (local.get $half) (i32.const 1)))))
    (local.set $a (call $input (i32.const {{.InputNumber "spcomb" "amount"}})))
    loop $bins
        (local.set $m (f32.const 0))
        (local.set $j (i32.const 0))
        loop $notes
            (local.set $r (f32.div
                (f32.mul (f32.convert_i32_u (local.get $k)) (local.get $binHz))
                (f32.load offset={{add (index .Labels "su_spectral") .SpectralScratch}} (i32.shl (local.get $j) (i32.const 2)))))
            (if (f32.ge (local.get $r) (f32.const 0.5)) (then
                (local.set $m (f32.max (local.get $m) (f32.max
                    (f32.sub (f32.const 1) (f32.mul (f32.abs (f32.sub (local.get $r) (f32.nearest (local.get $r)))) (local.get $sharp)))
                    (f32.const 0))))
            ))
            (br_if $notes (i32.lt_u (local.tee $j (i32.add (local.get $j) (i32.const 1))) (local.get $nf)))
        end
        (local.set $g (f32.add (f32.mul (f32.sub (local.get $m) (f32.const 1)) (local.get $a)) (f32.const 1)))
        (local.set $c (i32.const 0))
        loop $channels
            (local.set $p (i32.add (call $channelData (local.get $h) (local.get $c)) (i32.shl (local.get $k) (i32.const 3))))
            (f32.store (local.get $p) (f32.mul (f32.load (local.get $p)) (local.get $g)))
            (f32.store offset=4 (local.get $p) (f32.mul (f32.load offset=4 (local.get $p)) (local.get $g)))
            (br_if $channels (i32.lt_u (local.tee $c (i32.add (local.get $c) (i32.const 1))) (i32.load offset=12 (local.get $h))))
        end
        (br_if $bins (i32.le_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $half)))
    end
)
{{- end}}