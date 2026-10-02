;;-------------------------------------------------------------------------------
;;   su_run_vm function: runs the entire virtual machine once, creating 1 sample
;;-------------------------------------------------------------------------------
(func $su_run_vm (local $opcodeWithStereo i32) (local $opcode i32) (local $paramNum i32) (local $paramX4 i32) (local $WRKplusparam i32)
    loop $vm_loop
        (local.set $opcodeWithStereo (i32.load8_u (global.get $COM)))
        (global.set $COM (i32.add (global.get $COM) (i32.const 1)))  ;; move to next instruction
        (global.set $WRK (i32.add (global.get $WRK) (i32.const 64))) ;; move WRK to next unit
        (if (local.tee $opcode (i32.shr_u (local.get $opcodeWithStereo) (i32.const 1)))(then ;; if $opcode = $opcodeStereo >> 1; $opcode != 0 {
            (local.set $paramNum (i32.const 0))
            (local.set $paramX4 (i32.const 0))
            loop $transform_operands_loop
                {{- $addr := sub (index .Labels "su_vm_transformcounts") 1}}
                (if (i32.lt_u (local.get $paramNum) (i32.load8_u offset={{$addr}} (local.get $opcode)))(then ;;(i32.ge (local.get $paramNum) (i32.load8_u (local.get $opcode)))  /*TODO: offset to transformvalues
                    (local.set $WRKplusparam (i32.add (global.get $WRK) (local.get $paramX4)))
                    (f32.store offset={{index .Labels "su_transformedoperands"}}
                        (local.get $paramX4)
                        (f32.add
                            (f32.mul
                                (f32.convert_i32_u (call $scanOperand))
                                (f32.const 0.0078125) ;; scale from 0-128 to 0.0 - 1.0
                            )
                            (f32.load offset=32 (local.get $WRKplusparam)) ;; add modulation
                        )
                    )
                    (f32.store offset=32 (local.get $WRKplusparam) (f32.const 0.0)) ;; clear modulations
                    (local.set $paramNum (i32.add (local.get $paramNum) (i32.const 1))) ;; $paramNum++
                    (local.set $paramX4 (i32.add (local.get $paramX4) (i32.const 4)))
                    br $transform_operands_loop ;; continue looping
                ))
                ;; paramNum was >= the number of parameters to transform, exiting loop
            end
            (call_indirect (type $opcode_func_signature) (i32.and (local.get $opcodeWithStereo) (i32.const 1)) (local.get $opcode))
        )(else ;; advance to next voice
            (global.set $voice (i32.add (global.get $voice) (i32.const 4096))) ;; advance to next voice
            (global.set $WRK (global.get $voice)) ;; set WRK point to beginning of voice
            (global.set $voicesRemain (i32.sub (global.get $voicesRemain) (i32.const 1)))
{{- if .SupportsPolyphony}}
{{- if .WideVoices}}
            (if (i32.load8_u offset={{index .Labels "su_polyphony"}} (global.get $voicesRemain))(then
{{- else}}
            (if (i32.and (i32.shr_u (i32.const {{.PolyphonyBitmask | printf "%v"}}) (global.get $voicesRemain)) (i32.const 1))(then
{{- end}}
                (global.set $VAL (global.get $VAL_instr_start))
                (global.set $COM (global.get $COM_instr_start))
            ))
            (global.set $VAL_instr_start (global.get $VAL))
            (global.set $COM_instr_start (global.get $COM))
{{- end}}
            (br_if 2 (i32.eqz (global.get $voicesRemain))) ;; if no more voices remain, return from function
        ))
        br $vm_loop
    end
)

{{- template "arithmetic.wat" .}}
{{- template "effects.wat" .}}
{{- template "sources.wat" .}}
{{- template "sinks.wat" .}}
{{- template "spectral.wat" .}}
{{- template "mc.wat" .}}

;;-------------------------------------------------------------------------------
;; $input returns the float value of a transformed to 0.0 - 1.0f range.
;; The transformed values start at 512 (TODO: change magic constants somehow)
;;-------------------------------------------------------------------------------
(func $input (param $inputNumber i32) (result f32)
    (f32.load offset={{index .Labels "su_transformedoperands"}} (i32.mul (local.get $inputNumber) (i32.const 4)))
)

;;-------------------------------------------------------------------------------
;; $inputSigned returns the float value of a transformed to -1.0 - 1.0f range.
;;-------------------------------------------------------------------------------
(func $inputSigned (param $inputNumber i32) (result f32)
    (f32.sub (f32.mul (call $input (local.get $inputNumber)) (f32.const 2)) (f32.const 1))
)

;;-------------------------------------------------------------------------------
;; $nonLinearMap: x -> 2^(-24*input[x])
;;-------------------------------------------------------------------------------
(func $nonLinearMap (param $value i32) (result f32)
    (call $pow2
        (f32.mul
            (f32.const -24)
            (call $input (local.get $value))
        )
    )
)

;;-------------------------------------------------------------------------------
;; $pow2: x -> 2^x
;;-------------------------------------------------------------------------------
(func $pow2 (param $value f32) (result f32)
{{- if .MathImports}}
    (call $pow (f32.const 2) (local.get $value))
{{- else}}
    (call $exp2f (local.get $value))
{{- end}}
)
{{- if or (not .MathImports) .SpectralTable (.HasOp "ott") .MCTable}}

;;-------------------------------------------------------------------------------
;; $exp2f returns 2^y, for y clamped to [-126, 126]: 2 to the nearest integer
;; i of y times e^z, z = (y-i)·ln(2), from the Taylor series up to z⁶
(func $exp2f (param $y f32) (result f32) (local $i f32) (local $z f32) (local $p f32)
    (local.set $y (f32.min (select (local.get $y) (f32.const -126) (f32.gt (local.get $y) (f32.const -126))) (f32.const 126)))
    (local.set $i (f32.nearest (local.get $y)))
    (local.set $z (f32.mul (f32.sub (local.get $y) (local.get $i)) (f32.const 0.6931472)))
    (local.set $p (f32.add (f32.mul (local.get $z) (f32.const 0.0013888889)) (f32.const 0.008333334)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 0.041666668)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 0.16666667)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 0.5)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 1)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 1)))
    (f32.mul (local.get $p) (f32.reinterpret_i32 (i32.shl (i32.add (i32.trunc_f32_s (local.get $i)) (i32.const 127)) (i32.const 23))))
)
{{- end}}

{{- if .EnvelopeCurve}}

;; $exp2m1f returns 2^y - 1, also for y near 0, where $exp2f would lose its
;; digits: 2^i·(e^z - 1) + (2^i - 1), for i and z as in $exp2f, as exp2m1f in
;; vm/mathf.go.
(func $exp2m1f (param $y f32) (result f32) (local $i f32) (local $z f32) (local $p f32) (local $e f32)
    (local.set $y (f32.min (select (local.get $y) (f32.const -126) (f32.gt (local.get $y) (f32.const -126))) (f32.const 126)))
    (local.set $i (f32.nearest (local.get $y)))
    (local.set $z (f32.mul (f32.sub (local.get $y) (local.get $i)) (f32.const 0.6931472)))
    (local.set $p (f32.add (f32.mul (local.get $z) (f32.const 0.0013888889)) (f32.const 0.008333334)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 0.041666668)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 0.16666667)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 0.5)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $z)) (f32.const 1)))
    (local.set $e (f32.reinterpret_i32 (i32.shl (i32.add (i32.trunc_f32_s (local.get $i)) (i32.const 127)) (i32.const 23))))
    (f32.add (f32.mul (f32.mul (local.get $p) (local.get $z)) (local.get $e)) (f32.sub (local.get $e) (f32.const 1)))
)
{{- end}}

{{- if or (and (not .MathImports) (.HasOp "compressor")) .SpfilterTilt (.HasOp "spcompress") (.HasOp "spcross") (.HasOp "ott")}}
;; $log2f, $exp2f, $powf and $sinTurns are the float32 math functions of the
;; player, computed like log2f, exp2f, powf and sinTurns in vm/mathf.go,
;; operation by operation, so that the Go synth renders exactly the same.
;; Compiled with MathImports, the original units call Math.pow and Math.sin
;; of JavaScript instead; the spectral units always use these.
;;
;; $log2f returns the base 2 logarithm of x > 0: the exponent of x plus
;; ln(m)/ln(2) of its mantissa m in [√½, √2), from the series
;; ln(m) = 2(s + s³/3 + s⁵/5 + s⁷/7), s = (m-1)/(m+1)
(func $log2f (param $x f32) (result f32) (local $b i32) (local $e i32) (local $m f32) (local $s f32) (local $s2 f32) (local $p f32)
    (local.set $b (i32.reinterpret_f32 (local.get $x)))
    (local.set $e (i32.sub (i32.and (i32.shr_u (local.get $b) (i32.const 23)) (i32.const 0xff)) (i32.const 127)))
    (local.set $m (f32.reinterpret_i32 (i32.or (i32.and (local.get $b) (i32.const 0x7fffff)) (i32.const 0x3f800000))))
    (if (f32.gt (local.get $m) (f32.const 1.4142135)) (then
        (local.set $m (f32.mul (local.get $m) (f32.const 0.5)))
        (local.set $e (i32.add (local.get $e) (i32.const 1)))
    ))
    (local.set $s (f32.div (f32.sub (local.get $m) (f32.const 1)) (f32.add (local.get $m) (f32.const 1))))
    (local.set $s2 (f32.mul (local.get $s) (local.get $s)))
    (local.set $p (f32.add (f32.mul (local.get $s2) (f32.const 0.14285715)) (f32.const 0.2)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $s2)) (f32.const 0.33333334)))
    (local.set $p (f32.add (f32.mul (local.get $p) (local.get $s2)) (f32.const 1)))
    (f32.add (f32.mul (f32.mul (local.get $p) (local.get $s)) (f32.const 2.8853900)) (f32.convert_i32_s (local.get $e)))
)

;; $powf returns x^y for x > 0
(func $powf (param $x f32) (param $y f32) (result f32)
    (call $exp2f (f32.mul (local.get $y) (call $log2f (local.get $x))))
)
{{- end}}

{{- if or (and (not .MathImports) (or (.SupportsParamValue "oscillator" "type" .Sine) (.HasOp "belleq"))) .SpectralTable}}
;; $sinTurns returns sin(2π·t): t is in turns, not radians. t is folded to x in
;; [-1/4, 1/4] turns, where an odd polynomial fitted to sin(2π·x) gives it.
(func $sinTurns (param $t f32) (result f32) (local $x f32) (local $z f32)
    (local.set $x (f32.sub (local.get $t) (f32.nearest (local.get $t))))
    (if (f32.gt (local.get $x) (f32.const 0.25)) (then
        (local.set $x (f32.sub (f32.const 0.5) (local.get $x)))
    )(else (if (f32.lt (local.get $x) (f32.const -0.25)) (then
        (local.set $x (f32.sub (f32.const -0.5) (local.get $x)))
    ))))
    (local.set $z (f32.mul (local.get $x) (local.get $x)))
    (f32.mul
        (f32.add (f32.mul
            (f32.add (f32.mul
                (f32.add (f32.mul (local.get $z) (f32.const -70.9940414)) (f32.const 81.3408279))
                (local.get $z)) (f32.const -41.3371429))
            (local.get $z)) (f32.const 6.28316402))
        (local.get $x))
)
{{- end}}

;;-------------------------------------------------------------------------------
;; Waveshaper(x,a): "distorts" signal x by amount a
;; Returns  x*a/(1-a+(2*a-1)*abs(x))
;;-------------------------------------------------------------------------------
(func $waveshaper (param $signal f32) (param $amount f32) (result f32)
    (local.set $signal (call $clip (local.get $signal)))
    (f32.mul
        (local.get $signal)
        (f32.div
            (local.get $amount)
            (f32.add
                (f32.const 1)
                (f32.sub
                    (f32.mul
                        (f32.sub
                            (f32.add (local.get $amount) (local.get $amount))
                            (f32.const 1)
                        )
                        (f32.abs (local.get $signal))
                    )
                    (local.get $amount)
                )
            )
        )
    )
)

;;-------------------------------------------------------------------------------
;; Clip(a : f32) returns min(max(a,-1),1)
;;-------------------------------------------------------------------------------
(func $clip (param $value f32) (result f32)
    (f32.min (f32.max (local.get $value) (f32.const -1.0)) (f32.const 1.0))
)

(func $stereoHelper (param $stereo i32) (param $tableIndex i32)
    (if (local.get $stereo)(then
        (call $pop)
        (global.set $WRK (i32.add (global.get $WRK) (i32.const 16)))
        (call_indirect (type $opcode_func_signature) (i32.const 0) (local.get $tableIndex))
        (global.set $WRK (i32.sub (global.get $WRK) (i32.const 16)))
        (call $push)
    ))
)

;;-------------------------------------------------------------------------------
;; The opcode table jump table. This is constructed to only include the opcodes
;; that are used so that the jump table is as small as possible.
;;-------------------------------------------------------------------------------
(table {{.Instructions | len | add 1}} funcref)
(elem (i32.const 1) ;; start the indices at 1, as 0 is reserved for advance
{{- range .Instructions}}
    $su_op_{{.}}
{{- end}}
)
