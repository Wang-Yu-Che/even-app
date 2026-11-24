import type { ButtonHTMLAttributes, HTMLAttributes, ComponentProps } from 'react'
import { Badge as RadixBadge, Button as RadixButton, SegmentedControl as RadixSegmented, Switch, TextArea, TextField, Spinner } from '@radix-ui/themes'

export function Button({ variant = 'default', size, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'default' | 'highlight' | 'secondary' | 'ghost' | 'danger'; size?: 'sm' | 'lg' | 'icon' | 'default' }) {
  return <RadixButton {...props} size={size === 'sm' ? '1' : size === 'lg' ? '3' : '2'} variant={variant === 'secondary' ? 'soft' : variant === 'ghost' ? 'ghost' : 'solid'} color={variant === 'danger' ? 'red' : undefined} className={`desktop-button ${size === 'icon' ? 'icon-button' : ''} ${props.className ?? ''}`} />
}
export function Badge({ variant = 'neutral', ...props }: HTMLAttributes<HTMLSpanElement> & { variant?: 'positive' | 'negative' | 'accent' | 'neutral' }) {
  return <RadixBadge {...props} color={variant === 'positive' ? 'jade' : variant === 'negative' ? 'red' : variant === 'accent' ? 'blue' : 'gray'} radius="full" />
}
export function Card({ padding, ...props }: HTMLAttributes<HTMLDivElement> & { padding?: 'none' }) {
  return <div {...props} className={`desktop-card ${padding === 'none' ? '' : 'panel-padding'} ${props.className ?? ''}`} />
}
export function Input(props: ComponentProps<typeof TextField.Root>) { return <TextField.Root {...props} /> }
export function Textarea(props: ComponentProps<typeof TextArea>) { return <TextArea {...props} className={`desktop-textarea ${props.className ?? ''}`} /> }
export function Divider(props: HTMLAttributes<HTMLHRElement>) { return <hr {...props} className={`desktop-divider ${props.className ?? ''}`} /> }
export function Toggle({ checked, onChange, disabled, label }: { checked: boolean; onChange: (checked: boolean) => void; disabled?: boolean; label?: string }) {
  return <Switch checked={checked} onCheckedChange={onChange} disabled={disabled} aria-label={label} />
}
export function StatusDot({ connected }: { connected: boolean }) { return <span className={`status-dot ${connected ? 'is-connected' : ''}`} aria-hidden="true" /> }
export function Loading({ size = 14 }: { size?: number }) { return <Spinner style={{ width: size, height: size }} /> }
export function SegmentedControl({ options, value, onValueChange, className, size }: { options: { value: string; label: string }[]; value: string; onValueChange: (value: string) => void; className?: string; size?: 'small' }) {
  return <RadixSegmented.Root value={value} onValueChange={onValueChange} className={className} size={size === 'small' ? '1' : '2'}>{options.map(option => <RadixSegmented.Item key={option.value} value={option.value}>{option.label}</RadixSegmented.Item>)}</RadixSegmented.Root>
}
