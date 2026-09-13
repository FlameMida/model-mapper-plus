// 通知模块编辑器（T11）。固定全集按「统计周期 / 渠道窗口」两组展示；value 只含启用模块、
// 顺序即消息顺序（与 Go Notification.Modules 对齐）；勾选统计模块补默认 period: 'current'；
// 拖拽排序用 dnd-kit（spec 决策：模块顺序调整采用拖拽排序），未启用行手柄不绑 listeners。
// 周期选择用原生 <select>：仅统计模块行渲染；行锚定 data-module-row 供测试与父级定位。
import { DndContext, closestCenter, KeyboardSensor, PointerSensor, useSensor, useSensors } from '@dnd-kit/core'
import type { DragEndEvent } from '@dnd-kit/core'
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import { Checkbox } from '@douyinfe/semi-ui'
import type { CSSProperties } from 'react'
import {
  STAT_MODULE_KINDS,
  WINDOW_MODULE_KINDS,
  MODULE_LABELS,
  type ModuleConfig,
  type ModuleKind,
  type PeriodKind,
} from '../notifications'

export const PERIOD_LABELS: Record<PeriodKind, string> = { current: '本期累计', previous: '上一完整周期' }

/** 数据级重排（测试断言口径；拖拽 onDragEnd 亦复用，spec 口径不在 jsdom 模拟原生拖拽）。 */
export function reorderModules(mods: ModuleConfig[], from: number, to: number): ModuleConfig[] {
  return arrayMove(mods, from, to)
}

const GROUPS: { title: string; kinds: ModuleKind[] }[] = [
  { title: '统计周期', kinds: STAT_MODULE_KINDS },
  { title: '渠道窗口', kinds: WINDOW_MODULE_KINDS },
]

const rowStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  padding: '4px 10px',
  borderBottom: '1px solid var(--semi-color-border)',
}

const periodSelectStyle: CSSProperties = {
  marginLeft: 'auto',
  fontSize: 12,
  padding: '2px 6px',
  border: '1px solid var(--semi-color-border)',
  borderRadius: 3,
  color: 'var(--semi-color-text-0)',
  background: 'var(--semi-color-bg-0)',
}

const handleStyle: CSSProperties = {
  cursor: 'grab',
  color: 'var(--semi-color-text-2)',
  fontSize: 14,
  touchAction: 'none',
  userSelect: 'none',
}

interface RowProps {
  kind: ModuleKind
  config: ModuleConfig | null
  onToggle: (kind: ModuleKind, checked: boolean) => void
  onPeriod: (kind: ModuleKind, period: PeriodKind) => void
}

function ModuleRow({ kind, config, onToggle, onPeriod }: RowProps) {
  const enabled = config !== null
  const isStat = STAT_MODULE_KINDS.includes(kind)
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id: kind,
    disabled: !enabled,
  })
  return (
    <div
      ref={setNodeRef}
      data-module-row
      data-kind={kind}
      style={{
        ...rowStyle,
        opacity: enabled ? 1 : 0.55,
        // 等价 @dnd-kit/utilities 的 CSS.Translate.toString（该包未作为直接依赖安装，pnpm 严格布局不可直接 import）。
        transform: transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)` : undefined,
        transition,
        zIndex: isDragging ? 1 : undefined,
        background: isDragging ? 'var(--semi-color-primary-light-default)' : undefined,
      }}
    >
      <span
        ref={setActivatorNodeRef}
        {...(enabled ? { ...attributes, ...listeners } : {})}
        title={enabled ? '拖拽排序' : '勾选启用后可拖拽排序'}
        style={{ ...handleStyle, cursor: enabled ? 'grab' : 'not-allowed', color: 'var(--semi-color-text-2)' }}
      >
        ⠿
      </span>
      <Checkbox checked={enabled} onChange={(e) => onToggle(kind, e.target.checked === true)}>
        {MODULE_LABELS[kind]}
      </Checkbox>
      {isStat && !enabled && (
        <select aria-label={`${MODULE_LABELS[kind]}周期`} value="" disabled style={{ ...periodSelectStyle, color: 'var(--semi-color-text-2)' }}>
          <option value="">—</option>
        </select>
      )}
      {isStat && enabled && (
        <select
          aria-label={`${MODULE_LABELS[kind]}周期`}
          value={config.period ?? 'current'}
          onChange={(e) => onPeriod(kind, e.target.value as PeriodKind)}
          style={periodSelectStyle}
        >
          {(Object.keys(PERIOD_LABELS) as PeriodKind[]).map((p) => (
            <option key={p} value={p}>
              {PERIOD_LABELS[p]}
            </option>
          ))}
        </select>
      )}
    </div>
  )
}

export default function NotificationModulesEditor({ value, onChange }: {
  value: ModuleConfig[]
  onChange: (m: ModuleConfig[]) => void
}) {
  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const toggle = (kind: ModuleKind, checked: boolean) => {
    if (checked) {
      onChange([...value, STAT_MODULE_KINDS.includes(kind) ? { kind, period: 'current' } : { kind }])
    } else {
      onChange(value.filter((m) => m.kind !== kind))
    }
  }

  const setPeriod = (kind: ModuleKind, period: PeriodKind) => {
    onChange(value.map((m) => (m.kind === kind ? { ...m, period } : m)))
  }

  const onDragEnd = (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const ids = value.map((m) => m.kind)
    const from = ids.indexOf(active.id as ModuleKind)
    const to = ids.indexOf(over.id as ModuleKind)
    if (from < 0 || to < 0) return
    onChange(reorderModules(value, from, to))
  }

  return (
    <div>
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
        <SortableContext items={value.map((m) => m.kind)} strategy={verticalListSortingStrategy}>
          {GROUPS.map((group, gi) => (
            <section key={group.title} style={{ marginBottom: 12 }}>
              <div style={{ fontSize: 12, color: 'var(--semi-color-text-1)', marginBottom: 6 }}>
                {group.title}
                {gi === 0 && <span style={{ color: 'var(--semi-color-text-2)' }}>（勾选启用 · 行序即消息中的出现顺序）</span>}
              </div>
              <div style={{ border: '1px solid var(--semi-color-border)', borderRadius: 6, overflow: 'hidden' }}>
                {[
                  ...value
                    .filter((m) => group.kinds.includes(m.kind))
                    .map((config) => ({ kind: config.kind, config })),
                  ...group.kinds
                    .filter((k) => !value.some((m) => m.kind === k))
                    .map((kind) => ({ kind, config: null as ModuleConfig | null })),
                ].map(({ kind, config }) => (
                  <ModuleRow key={kind} kind={kind} config={config} onToggle={toggle} onPeriod={setPeriod} />
                ))}
              </div>
            </section>
          ))}
        </SortableContext>
      </DndContext>
    </div>
  )
}
