.PHONY: check
check:
	pnpm test
	pnpm build
	pnpm verify
