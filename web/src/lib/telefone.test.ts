import { describe, expect, it } from 'vitest'

import { formatarTelefone, soDigitos } from './telefone'

describe('soDigitos', () => {
  it('remove tudo que não é dígito', () => {
    expect(soDigitos('(83) 9 9999-9999')).toBe('83999999999')
    expect(soDigitos('83 99999 9999')).toBe('83999999999')
    expect(soDigitos('')).toBe('')
  })
})

describe('formatarTelefone', () => {
  it('celular de 11 dígitos: (83) 9 9999-9999', () => {
    expect(formatarTelefone('83999999999')).toBe('(83) 9 9999-9999')
  })

  it('fixo de 10 dígitos: (83) 3333-4444', () => {
    expect(formatarTelefone('8333334444')).toBe('(83) 3333-4444')
  })

  it('formata entrada que já vem mascarada (idempotente)', () => {
    // O v-model chama isto sobre o próprio valor guardado (dígitos), mas se receber
    // texto mascarado ele não pode duplicar máscara.
    expect(formatarTelefone('(83) 9 9999-9999')).toBe('(83) 9 9999-9999')
  })

  it('progressivo: nunca explode com entrada parcial', () => {
    expect(formatarTelefone('')).toBe('')
    expect(formatarTelefone('8')).toBe('(8')
    expect(formatarTelefone('83')).toBe('(83')
    expect(formatarTelefone('839')).toBe('(83) 9')
    expect(formatarTelefone('8399')).toBe('(83) 99')
    expect(formatarTelefone('83999')).toBe('(83) 999')
  })

  it('descarta dígitos além de 11', () => {
    // Colar um número com lixo no fim não pode gerar uma máscara maior que o campo.
    expect(formatarTelefone('839999999990000')).toBe('(83) 9 9999-9999')
  })
})
