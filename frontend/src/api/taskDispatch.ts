import request from '../utils/request'
import type { ExternalTaskContact, Task } from './task'

export interface TaskDispatchBatch {
  id: number
  token: string
  project_id: number
  contact_id: number
  contact?: ExternalTaskContact
  status: 'pending' | 'sent' | 'received' | 'failed'
  task_count: number
  dispatch_date?: string
  sent_at?: string
  received_at?: string
  success_count: number
  failure_count: number
  error_message?: string
}

export interface TaskTimeChangeRequest {
  id: number
  task_id: number
  task?: Task
  requested_start?: string
  requested_end?: string
  reason: string
  status: 'pending' | 'approved' | 'rejected'
  reviewer_id: number
  created_at: string
}

export const getTaskContacts = (projectId: number): Promise<ExternalTaskContact[]> => request.get(`/projects/${projectId}/task-contacts`)
export const createTaskContact = (projectId: number, data: Partial<ExternalTaskContact>) => request.post(`/projects/${projectId}/task-contacts`, data)
export const updateTaskContact = (projectId: number, id: number, data: Partial<ExternalTaskContact>) => request.put(`/projects/${projectId}/task-contacts/${id}`, data)
export const deleteTaskContact = (projectId: number, id: number) => request.delete(`/projects/${projectId}/task-contacts/${id}`)
export const sendTaskDispatch = (projectId: number): Promise<{ batches: TaskDispatchBatch[]; count: number }> => request.post(`/projects/${projectId}/task-dispatches/send`)
export const getTaskDispatches = (projectId: number): Promise<TaskDispatchBatch[]> => request.get(`/projects/${projectId}/task-dispatches`)
export const importTaskDispatch = (file: File) => {
  const data = new FormData()
  data.append('file', file)
  return request.post('/task-dispatch/import', data, { headers: { 'Content-Type': 'multipart/form-data' } })
}
export const getTimeChangeRequests = (status?: string): Promise<TaskTimeChangeRequest[]> => request.get('/task-dispatch/time-change-requests', { params: { status } })
export const reviewTimeChangeRequest = (id: number, decision: 'approved' | 'rejected', comment = '') => request.post(`/task-dispatch/time-change-requests/${id}/review`, { decision, comment })
