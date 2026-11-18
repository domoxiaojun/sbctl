import { useEffect, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { Schema } from '@/api/types'

export function useNodeCommand(nodeId: string) {
  const client = useQueryClient(), storageKey = `sbctl-active-node-job:${nodeId}`
  const [jobId, setJobId] = useState(() => { try { return sessionStorage.getItem(storageKey) ?? '' } catch { return '' } })
  const handled = useRef('')
  const job = useQuery({ queryKey: ['node-job', jobId], queryFn: ({ signal }) => api.nodeJob(jobId, signal), enabled: !!jobId, refetchInterval: q => ['queued', 'running'].includes(q.state.data?.state ?? '') ? 1000 : false })
  const submit = useMutation({ gcTime: 0, mutationFn: ({ input, key }: { input: Schema['NodeJobInput']; key: string }) => api.submitNodeJob(input, key), onSuccess: async result => {
    setJobId(result.job_id)
    try { sessionStorage.setItem(storageKey, result.job_id) } catch { /* Result remains visible in server task history. */ }
    await Promise.all([
      client.invalidateQueries({ queryKey: ['node-jobs', nodeId] }),
      client.invalidateQueries({ queryKey: ['node-config', nodeId] }),
    ])
  } })
  useEffect(() => {
    if (!job.data || !['completed', 'failed'].includes(job.data.state)) return
    const signature = `${job.data.id}:${job.data.state}`
    if (handled.current === signature) return
    handled.current = signature
    for (const key of [['node-config', nodeId], ['node', nodeId], ['nodes'], ['node-jobs', nodeId]]) void client.invalidateQueries({ queryKey: key })
  }, [job.data, client, nodeId])
  return { submit, job, busy: submit.isPending || ['queued', 'running'].includes(job.data?.state ?? '') }
}
