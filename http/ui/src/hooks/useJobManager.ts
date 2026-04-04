import { useState, useEffect, useCallback, useRef } from 'react'
import type { Job, Progress, ScanResult, CreateJobResponse, ActiveJobError } from '../types'

const STORAGE_KEY = 'recon_job_id'

const JOB_PATH_RE = /^\/job\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\/?$/i

function getJobIdFromURL(): string | null {
  const match = JOB_PATH_RE.exec(globalThis.location.pathname)
  return match ? match[1] : null
}

function updateURLPath(jobId: string | null) {
  const target = jobId ? `/job/${jobId}` : '/'
  if (globalThis.location.pathname !== target) {
    globalThis.history.replaceState({}, '', target)
  }
}

interface UseJobManagerReturn {
  jobId: string | null
  job: Job | null
  progress: Progress | null
  result: ScanResult | null
  error: string | null
  isLoading: boolean
  isExpired: boolean
  jobUrl: string | null
  startScan: (url: string) => Promise<void>
  clearJob: () => void
}

export function useJobManager(): UseJobManagerReturn {
  const [jobId, setJobId] = useState<string | null>(() => {
    // Only use URL path for initial state (direct link).
    // localStorage is used to resume polling, but we don't push it to the URL on load.
    return getJobIdFromURL() || localStorage.getItem(STORAGE_KEY)
  })
  // Track whether the job ID came from a direct link vs localStorage
  const [fromURL] = useState(() => !!getJobIdFromURL())
  const [job, setJob] = useState<Job | null>(null)
  const [progress, setProgress] = useState<Progress | null>(null)
  const [result, setResult] = useState<ScanResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(false)
  const [isExpired, setIsExpired] = useState(false)
  const eventSourceRef = useRef<EventSource | null>(null)

  // Compute shareable URL for current job
  const jobUrl = jobId
    ? `${globalThis.location.origin}/job/${jobId}`
    : null

  // Track whether the URL should be updated (true after user starts a scan or opens a direct link)
  const shouldSyncURL = useRef(fromURL)

  // Save job ID to localStorage; only sync URL when appropriate
  useEffect(() => {
    if (jobId) {
      localStorage.setItem(STORAGE_KEY, jobId)
      if (shouldSyncURL.current) {
        updateURLPath(jobId)
      }
    } else {
      localStorage.removeItem(STORAGE_KEY)
      updateURLPath(null)
    }
  }, [jobId])

  const fetchJobStatus = useCallback(async (id: string) => {
    try {
      const response = await fetch(`/api/jobs/${id}`)
      if (response.status === 404) {
        // Job no longer exists (expired from cache)
        setIsExpired(true)
        setIsLoading(false)
        return
      }

      const jobData: Job = await response.json()
      setJob(jobData)

      if (jobData.status === 'completed') {
        setResult(jobData.result || null)
        setProgress(null)
        setIsLoading(false)
      } else if (jobData.status === 'failed') {
        setError(jobData.error || 'Job failed')
        setProgress(null)
        setIsLoading(false)
      } else if (jobData.status === 'running' || jobData.status === 'pending') {
        setIsLoading(true)
        setProgress(jobData.progress || null)
        subscribeToProgress(id)
      }
    } catch {
      setError('Failed to fetch job status')
    }
  }, [])

  const subscribeToProgress = useCallback((id: string) => {
    // Close existing connection if any
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
    }

    const eventSource = new EventSource(`/api/jobs/${id}/stream`)
    eventSourceRef.current = eventSource

    eventSource.addEventListener('progress', (event) => {
      const data: Progress = JSON.parse(event.data)
      setProgress(data)
    })

    eventSource.addEventListener('complete', async (event) => {
      const data = JSON.parse(event.data)
      eventSource.close()
      eventSourceRef.current = null
      setIsLoading(false)

      if (data.status === 'completed') {
        // Fetch final result
        const response = await fetch(`/api/jobs/${id}`)
        const jobData: Job = await response.json()
        setJob(jobData)
        setResult(jobData.result || null)
        setProgress(null)
      } else if (data.status === 'failed') {
        setError(data.error || 'Scan failed')
        setProgress(null)
      }
    })

    eventSource.addEventListener('error', () => {
      eventSource.close()
      eventSourceRef.current = null
      setIsLoading(false)
      // Try to fetch final status
      fetchJobStatus(id)
    })
  }, [fetchJobStatus])

  // Fetch job status on mount if we have a stored job ID
  useEffect(() => {
    if (jobId) {
      fetchJobStatus(jobId)
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const startScan = async (url: string) => {
    shouldSyncURL.current = true
    setError(null)
    setResult(null)
    setIsExpired(false)
    setProgress({ stage: 'Creating job...', current: 0, total: 0 })
    setIsLoading(true)

    try {
      const response = await fetch('/api/jobs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ url }),
      })

      if (response.status === 409) {
        // Already have an active job
        const data = await response.json() as ActiveJobError
        setJobId(data.active_job_id)
        setError('You already have an active scan running')
        fetchJobStatus(data.active_job_id)
        return
      }

      if (!response.ok) {
        const data = await response.json()
        throw new Error(data.error || 'Failed to create job')
      }

      const data: CreateJobResponse = await response.json()
      setJobId(data.job_id)
      setProgress({ stage: 'Connecting...', current: 0, total: 0 })
      subscribeToProgress(data.job_id)
    } catch (err) {
      setIsLoading(false)
      setProgress(null)
      setError(err instanceof Error ? err.message : 'An error occurred')
    }
  }

  const clearJob = useCallback(() => {
    if (eventSourceRef.current) {
      eventSourceRef.current.close()
      eventSourceRef.current = null
    }
    setJobId(null)
    setJob(null)
    setProgress(null)
    setResult(null)
    setError(null)
    setIsLoading(false)
    setIsExpired(false)
  }, [])

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      if (eventSourceRef.current) {
        eventSourceRef.current.close()
      }
    }
  }, [])

  return {
    jobId,
    job,
    progress,
    result,
    error,
    isLoading,
    isExpired,
    jobUrl,
    startScan,
    clearJob,
  }
}
