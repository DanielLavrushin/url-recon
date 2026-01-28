export interface DomainInfo {
  domain: string
  ips: string[] | null
  cdn: string
  count: number
  sources: string[]
  external: boolean
}

export interface Stats {
  total_domains: number
  external_domains: number
  internal_domains: number
}

export interface ScanResult {
  target_url: string
  target_domain: string
  domains: DomainInfo[] | null
  stats: Stats
  scanned_at: string
}

export interface Progress {
  stage: string
  current: number
  total: number
}

// Job management types
export type JobStatus = 'pending' | 'running' | 'completed' | 'failed'

export interface Job {
  id: string
  url: string
  status: JobStatus
  progress?: Progress
  result?: ScanResult
  error?: string
  created_at: string
  started_at?: string
  ended_at?: string
}

export interface CreateJobResponse {
  job_id: string
  status: JobStatus
  message: string
}

export interface ActiveJobError {
  error: string
  code: 'ACTIVE_JOB_EXISTS'
  active_job_id: string
}
