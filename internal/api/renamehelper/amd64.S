// Linux x86-64: renameat2(AT_FDCWD, argv[1], AT_FDCWD, argv[2], RENAME_NOREPLACE).
// Freestanding: exit 0 on success, otherwise the positive Linux errno.
.text
.global _start
_start:
    cmpq $3, (%rsp)
    jne bad_args
    movq $316, %rax
    movq $-100, %rdi
    movq 16(%rsp), %rsi
    movq $-100, %rdx
    movq 24(%rsp), %r10
    movq $1, %r8
    syscall
    negq %rax
    movq %rax, %rdi
exit:
    movq $60, %rax
    syscall
bad_args:
    movq $64, %rdi
    jmp exit
