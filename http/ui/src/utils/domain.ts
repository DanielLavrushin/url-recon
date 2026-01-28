// Common second-level domains that indicate a two-part TLD
const SECOND_LEVEL_TLDS = new Set([
  'co', 'com', 'org', 'net', 'gov', 'edu', 'ac', 'or', 'ne', 'go', 'gob', 'nic'
])

export function getRootDomain(domain: string): string {
  const parts = domain.split('.')
  if (parts.length <= 2) return domain

  // Check for two-part TLDs like .co.uk, .com.tr
  const secondLast = parts[parts.length - 2]
  if (SECOND_LEVEL_TLDS.has(secondLast) && parts.length > 2) {
    return parts.slice(-3).join('.')
  }

  return parts.slice(-2).join('.')
}

export function normalizeDomain(domain: string): string {
  // Remove www. prefix
  return domain.replace(/^www\./, '')
}

export function sortIPs(ips: string[]): string[] {
  return [...ips].sort((a, b) => {
    const aParts = a.split('.').map(Number)
    const bParts = b.split('.').map(Number)
    for (let i = 0; i < 4; i++) {
      if (aParts[i] !== bParts[i]) return aParts[i] - bParts[i]
    }
    return 0
  })
}
