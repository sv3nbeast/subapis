import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import DataTable from '../DataTable.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const messages: Record<string, string> = {
    'admin.accounts.columns.createdAt': '创建时间',
    'empty.noData': '暂无数据'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
      te: (key: string) => Object.prototype.hasOwnProperty.call(messages, key)
    })
  }
})

describe('DataTable', () => {
  it('resolves raw i18n column keys before rendering headers', () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [
          { key: 'created_at', label: 'admin.accounts.columns.createdAt', sortable: true },
          { key: 'model', label: 'Model', sortable: false }
        ],
        data: [
          { id: 1, created_at: '2026-06-01T00:00:00Z', model: 'claude-opus-4-8' }
        ],
        rowKey: 'id'
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    expect(wrapper.text()).toContain('创建时间')
    expect(wrapper.text()).not.toContain('admin.accounts.columns.createdAt')
  })
})
