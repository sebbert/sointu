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
;;   SPFFT opcode: pops a signal into the ring, and every size/4 samples
;;   replaces the spectrum with the FFT of the last size samples, windowed.
;;-------------------------------------------------------------------------------
(func $su_op_spfft (param $stereo i32) (local $in f32) (local $u i32) (local $st i32) (local $h i32) (local $x i32) (local $n i32) (local $pos i32) (local $j i32)
    (local.set $in (call $pop))
    (local.set $u (call $spectralUnit))
    (if (i32.eqz (call $spectralVoice (local.get $u))) (then
        return
    ))
    (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
    (local.set $h (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=8 (local.get $u))))
    (local.set $n (i32.shl (i32.const 1) (i32.load offset=4 (local.get $h))))
    (local.set $pos (i32.load (local.get $st)))
    (f32.store offset=16 (i32.add (local.get $st) (i32.shl (local.get $pos) (i32.const 2))) (local.get $in))
    (i32.store (local.get $st) (local.tee $pos (i32.and (i32.add (local.get $pos) (i32.const 1)) (i32.sub (local.get $n) (i32.const 1)))))
    (if (i32.and (local.get $pos) (i32.sub (i32.shr_u (local.get $n) (i32.const 2)) (i32.const 1))) (then
        return
    ))
    (local.set $x (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load (local.get $h))))
    loop $window
        (f32.store (i32.add (local.get $x) (i32.shl (local.get $j) (i32.const 3))) (f32.mul
            (f32.load offset=16 (i32.add (local.get $st) (i32.shl
                (i32.and (i32.add (local.get $pos) (local.get $j)) (i32.sub (local.get $n) (i32.const 1)))
                (i32.const 2))))
            (call $hann (local.get $j) (i32.load offset=4 (local.get $h)))
        ))
        (f32.store offset=4 (i32.add (local.get $x) (i32.shl (local.get $j) (i32.const 3))) (f32.const 0))
        (br_if $window (i32.lt_u (local.tee $j (i32.add (local.get $j) (i32.const 1))) (local.get $n)))
    end
    (call $fft (local.get $x) (local.get $n))
    (i32.store offset=8 (local.get $h) (i32.add (i32.load offset=8 (local.get $h)) (i32.const 1)))
)
{{- end}}

{{- if .HasOp "spifft"}}
;;-------------------------------------------------------------------------------
;;   SPIFFT opcode: overlap-adds each new spectrum, transformed back and
;;   windowed, to the ring, and pushes the next sample of the ring times gain.
;;   The inverse FFT of a spectrum with conjugate symmetry is real:
;;   real(ifft(X)) = real(fft(conj(X)))/n, where the bins above n/2 mirror the
;;   bins below it.
;;-------------------------------------------------------------------------------
(func $su_op_spifft (param $stereo i32) (local $u i32) (local $st i32) (local $h i32) (local $x i32) (local $s i32) (local $n i32) (local $pos i32) (local $k i32) (local $p i32) (local $scale f32)
    (local.set $u (call $spectralUnit))
    (if (i32.eqz (call $spectralVoice (local.get $u))) (then
        (call $push (f32.const 0))
        return
    ))
    (local.set $st (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load offset=4 (local.get $u))))
    (local.set $h (i32.add (i32.const {{index .Labels "su_spectrum_table"}}) (i32.load offset=8 (local.get $u))))
    (local.set $n (i32.shl (i32.const 1) (i32.load offset=4 (local.get $h))))
    (local.set $pos (i32.load (local.get $st)))
    (if (i32.ne (i32.load offset=8 (local.get $h)) (i32.load offset=4 (local.get $st))) (then
        (i32.store offset=4 (local.get $st) (i32.load offset=8 (local.get $h)))
        (local.set $x (i32.add (i32.const {{index .Labels "su_spectral"}}) (i32.load (local.get $h))))
        (local.set $s (i32.const {{add (index .Labels "su_spectral") .SpectralScratch}}))
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
        ;; the squared Hann windows overlapping by 3/4 sum to 3/2
        (local.set $scale (f32.div (f32.const 0.6666667) (f32.convert_i32_u (local.get $n))))
        (local.set $k (i32.const 0))
        loop $overlapAdd
            (local.set $p (i32.add (local.get $st) (i32.shl
                (i32.and (i32.add (local.get $pos) (local.get $k)) (i32.sub (local.get $n) (i32.const 1)))
                (i32.const 2))))
            (f32.store offset=16 (local.get $p) (f32.add
                (f32.load offset=16 (local.get $p))
                (f32.mul
                    (f32.mul (f32.load (i32.add (local.get $s) (i32.shl (local.get $k) (i32.const 3)))) (local.get $scale))
                    (call $hann (local.get $k) (i32.load offset=4 (local.get $h))))
            ))
            (br_if $overlapAdd (i32.lt_u (local.tee $k (i32.add (local.get $k) (i32.const 1))) (local.get $n)))
        end
    ))
    (local.set $p (i32.add (local.get $st) (i32.shl (local.get $pos) (i32.const 2))))
    (call $push (f32.mul (f32.load offset=16 (local.get $p)) (call $input (i32.const {{.InputNumber "spifft" "gain"}}))))
    (f32.store offset=16 (local.get $p) (f32.const 0))
    (i32.store (local.get $st) (i32.and (i32.add (local.get $pos) (i32.const 1)) (i32.sub (local.get $n) (i32.const 1))))
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
        (i32.shl (i32.const 8) (select
            (i32.load offset=4 (local.get $h))
            (i32.load offset=4 (local.get $src))
            (i32.lt_u (i32.load offset=4 (local.get $h)) (i32.load offset=4 (local.get $src)))
        ))
    )
    (i32.store offset=8 (local.get $h) (i32.add (i32.load offset=8 (local.get $h)) (i32.const 1)))
)
{{- end}}