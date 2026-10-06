.PHONY: check
check:
	pnpm test
	pnpm build
	pnpm verify
	node scripts/verify-languages.mjs
