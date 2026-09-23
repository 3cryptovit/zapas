import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { StatusBadge } from './StatusBadge'

describe('StatusBadge', () => {
  it('показывает текст статуса, а не только цвет', () => {
    render(<StatusBadge status="critical" />)
    expect(screen.getByText('Критично')).toBeInTheDocument()
  })

  it('прячет иконку от скринридера — текст её уже дублирует', () => {
    const { container } = render(<StatusBadge status="ok" />)
    expect(container.querySelector('[aria-hidden="true"]')).toBeTruthy()
    expect(screen.getByText('Хватает')).toBeInTheDocument()
  })
})
