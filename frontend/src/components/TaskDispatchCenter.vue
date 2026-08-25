<template>
  <a-modal :open="open" title="任务 Excel 收发中心" width="1100px" :footer="null" @cancel="$emit('update:open', false)">
    <a-alert message="系统每天18:00自动下发进行中、已延期和今日开始的任务；收到回邮附件后自动汇总。" type="info" show-icon style="margin-bottom:16px" />
    <a-tabs v-model:activeKey="activeTab" @change="loadActiveTab">
      <a-tab-pane key="contacts" tab="乙方联系人">
        <a-space style="margin-bottom:12px">
          <a-button type="primary" @click="editContact()">新增联系人</a-button>
          <a-button @click="loadContacts">刷新</a-button>
        </a-space>
        <a-table :data-source="contacts" :columns="contactColumns" row-key="id" :pagination="false" size="small">
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'enabled'"><a-tag :color="record.enabled ? 'green' : 'default'">{{ record.enabled ? '启用' : '停用' }}</a-tag></template>
            <template v-else-if="column.key === 'action'">
              <a-space><a-button type="link" @click="editContact(record)">编辑</a-button><a-popconfirm title="确定删除/停用此联系人？" @confirm="removeContact(record)"><a-button type="link" danger>删除</a-button></a-popconfirm></a-space>
            </template>
          </template>
        </a-table>
      </a-tab-pane>
      <a-tab-pane key="dispatches" tab="下发与回收记录">
        <a-space style="margin-bottom:12px">
          <a-button type="primary" :loading="sending" @click="sendNow">立即下发今日任务</a-button>
          <a-upload :show-upload-list="false" accept=".xlsx" :before-upload="importExcel"><a-button>手动导入回收 Excel</a-button></a-upload>
          <a-button @click="loadBatches">刷新</a-button>
        </a-space>
        <a-table :data-source="batches" :columns="batchColumns" row-key="id" size="small" :pagination="{ pageSize: 10 }">
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'contact'">{{ record.contact?.name || '-' }}</template>
            <template v-else-if="column.key === 'status'"><a-tag :color="batchColor(record.status)">{{ batchText(record.status) }}</a-tag></template>
            <template v-else-if="column.key === 'result'">成功 {{ record.success_count || 0 }} / 失败 {{ record.failure_count || 0 }}<div v-if="record.error_message" class="error-text">{{ record.error_message }}</div></template>
          </template>
        </a-table>
      </a-tab-pane>
      <a-tab-pane key="approvals">
        <template #tab><a-badge :count="pendingApprovalCount" :offset="[8, -2]">时间变更审核</a-badge></template>
        <a-space style="margin-bottom:12px"><a-button @click="loadApprovals">刷新</a-button></a-space>
        <a-table :data-source="approvals" :columns="approvalColumns" row-key="id" size="small" :pagination="{ pageSize: 10 }">
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'task'">{{ record.task?.title || `任务#${record.task_id}` }}</template>
            <template v-else-if="column.key === 'dates'">{{ shortDate(record.requested_start) || '-' }} → {{ shortDate(record.requested_end) || '-' }}</template>
            <template v-else-if="column.key === 'status'"><a-tag :color="record.status === 'pending' ? 'orange' : record.status === 'approved' ? 'green' : 'red'">{{ record.status === 'pending' ? '待审核' : record.status === 'approved' ? '已通过' : '已驳回' }}</a-tag></template>
            <template v-else-if="column.key === 'action' && record.status === 'pending'"><a-space><a-button type="link" @click="review(record.id, 'approved')">通过</a-button><a-button type="link" danger @click="review(record.id, 'rejected')">驳回</a-button></a-space></template>
          </template>
        </a-table>
      </a-tab-pane>
    </a-tabs>
  </a-modal>

  <a-modal v-model:open="contactModal" :title="contactForm.id ? '编辑乙方联系人' : '新增乙方联系人'" @ok="saveContact">
    <a-form layout="vertical">
      <a-form-item label="负责人/分组名称" required><a-input v-model:value="contactForm.name" placeholder="例如：瑞熙" /></a-form-item>
      <a-form-item label="公司/部门"><a-input v-model:value="contactForm.company" /></a-form-item>
      <a-form-item label="收件人姓名"><a-input v-model:value="contactForm.recipient" /></a-form-item>
      <a-form-item label="收件邮箱" required><a-input v-model:value="contactForm.email" /></a-form-item>
      <a-form-item label="抄送邮箱"><a-input v-model:value="contactForm.cc_emails" placeholder="多个邮箱用逗号分隔" /></a-form-item>
      <a-form-item label="状态"><a-switch v-model:checked="contactForm.enabled" checked-children="启用" un-checked-children="停用" /></a-form-item>
      <a-form-item label="备注"><a-textarea v-model:value="contactForm.notes" :rows="2" /></a-form-item>
    </a-form>
  </a-modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { message, Modal } from 'ant-design-vue'
import type { ExternalTaskContact } from '@/api/task'
import {
  createTaskContact, deleteTaskContact, getTaskContacts, getTaskDispatches,
  getTimeChangeRequests, importTaskDispatch, reviewTimeChangeRequest,
  sendTaskDispatch, updateTaskContact, type TaskDispatchBatch, type TaskTimeChangeRequest
} from '@/api/taskDispatch'

const props = defineProps<{ open: boolean; projectId: number }>()
const emit = defineEmits<{ 'update:open': [value: boolean]; contactsChanged: [contacts: ExternalTaskContact[]] }>()
const activeTab = ref('contacts')
const contacts = ref<ExternalTaskContact[]>([])
const batches = ref<TaskDispatchBatch[]>([])
const approvals = ref<TaskTimeChangeRequest[]>([])
const pendingApprovalCount = computed(() => approvals.value.filter(item => item.status === 'pending').length)
const sending = ref(false)
const contactModal = ref(false)
const contactForm = reactive<Partial<ExternalTaskContact>>({ enabled: true })
const contactColumns = [{ title: '负责人/分组', dataIndex: 'name' }, { title: '公司/部门', dataIndex: 'company' }, { title: '收件人', dataIndex: 'recipient' }, { title: '邮箱', dataIndex: 'email' }, { title: '状态', key: 'enabled', width: 80 }, { title: '操作', key: 'action', width: 130 }]
const batchColumns = [{ title: '日期', dataIndex: 'dispatch_date', width: 110 }, { title: '联系人', key: 'contact' }, { title: '任务数', dataIndex: 'task_count', width: 80 }, { title: '状态', key: 'status', width: 90 }, { title: '导入结果', key: 'result' }, { title: '发送时间', dataIndex: 'sent_at' }]
const approvalColumns = [{ title: '任务', key: 'task' }, { title: '申请日期', key: 'dates', width: 210 }, { title: '原因', dataIndex: 'reason' }, { title: '状态', key: 'status', width: 90 }, { title: '操作', key: 'action', width: 120 }]

watch(() => props.open, value => { if (value) { loadActiveTab(); loadApprovals() } })
const loadContacts = async () => { contacts.value = await getTaskContacts(props.projectId); emit('contactsChanged', contacts.value) }
const loadBatches = async () => { batches.value = await getTaskDispatches(props.projectId) }
const loadApprovals = async () => { approvals.value = await getTimeChangeRequests() }
const loadActiveTab = () => activeTab.value === 'contacts' ? loadContacts() : activeTab.value === 'dispatches' ? loadBatches() : loadApprovals()
const editContact = (contact?: ExternalTaskContact) => { Object.assign(contactForm, { id: undefined, name: '', company: '', recipient: '', email: '', cc_emails: '', enabled: true, notes: '' }, contact || {}); contactModal.value = true }
const saveContact = async () => {
  if (!contactForm.name?.trim() || !contactForm.email?.trim()) return message.warning('请填写负责人名称和邮箱')
  contactForm.id ? await updateTaskContact(props.projectId, contactForm.id, contactForm) : await createTaskContact(props.projectId, contactForm)
  message.success('保存成功'); contactModal.value = false; await loadContacts()
}
const removeContact = async (contact: ExternalTaskContact) => { await deleteTaskContact(props.projectId, contact.id); message.success('已处理'); await loadContacts() }
const sendNow = async () => { sending.value = true; try { const result = await sendTaskDispatch(props.projectId); message.success(result.count ? `已发送 ${result.count} 个批次` : '今天没有新的待下发任务，或今日已发送') ; await loadBatches() } finally { sending.value = false } }
const importExcel = async (file: File) => { await importTaskDispatch(file); message.success('Excel已汇总，日期变更已进入审核'); await loadBatches(); return false }
const review = (id: number, decision: 'approved' | 'rejected') => Modal.confirm({ title: decision === 'approved' ? '确认通过时间变更？' : '确认驳回时间变更？', onOk: async () => { await reviewTimeChangeRequest(id, decision); message.success('审核完成'); await loadApprovals() } })
const shortDate = (value?: string) => value?.slice(0, 10)
const batchText = (value: string) => ({ pending: '待发送', sent: '已发送', received: '已回收', failed: '失败' }[value] || value)
const batchColor = (value: string) => ({ pending: 'orange', sent: 'blue', received: 'green', failed: 'red' }[value] || 'default')
</script>

<style scoped>.error-text{color:#ff4d4f;font-size:12px;max-width:320px;white-space:normal}</style>
