interface WorkArea {
  availWidth: number
  availHeight: number
  availLeft?: number
  availTop?: number
}

/** Screen dimensions and window features use CSS pixels, already OS-scaled. */
export function logWindowFeatures(screen: WorkArea): string {
  const valid = Number.isFinite(screen.availWidth) && screen.availWidth > 0 && Number.isFinite(screen.availHeight) && screen.availHeight > 0
  const width = valid ? Math.floor(screen.availWidth) : 1100
  const height = valid ? Math.max(1, Math.floor(screen.availHeight * 0.9)) : 750
  const left = Number.isFinite(screen.availLeft) ? Math.floor(screen.availLeft!) : 0
  const top = (Number.isFinite(screen.availTop) ? Math.floor(screen.availTop!) : 0) + (valid ? Math.floor((screen.availHeight - height) / 2) : 0)
  return `popup,width=${width},height=${height},left=${left},top=${top}`
}
