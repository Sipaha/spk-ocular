// Linux AArch64: renameat2(AT_FDCWD, argv[1], AT_FDCWD, argv[2], RENAME_NOREPLACE).
// Freestanding: exit 0 on success, otherwise the positive Linux errno.
.text
.global _start
_start:
    ldr x0, [sp]
    cmp x0, #3
    b.ne bad_args
    mov x0, #-100
    ldr x1, [sp, #16]
    mov x2, #-100
    ldr x3, [sp, #24]
    mov x4, #1
    mov x8, #276
    svc #0
    neg x0, x0
exit:
    mov x8, #93
    svc #0
bad_args:
    mov x0, #64
    b exit
