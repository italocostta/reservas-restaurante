/**
 * Máscara de telefone brasileiro para EXIBIÇÃO. O valor guardado e enviado à API
 * é sempre só os dígitos — ver `soDigitos`.
 *
 * Por que separar dígito de máscara: o backend não valida telefone (débito 5 da
 * spec: texto livre na v1), e o link `tel:` do ListaReservas precisa de dígitos
 * limpos para o discador funcionar. Guardar "(83) 9 9999-9999" quebraria o tel: e
 * ainda amarraria o dado ao formato de UM país. A máscara é roupa; o dado é o
 * número.
 */

/** Tira tudo que não é dígito. "(83) 9 9999-9999" → "83999999999". */
export function soDigitos(s: string): string {
  return s.replace(/\D/g, '')
}

/**
 * Formata progressivamente, para caber num handler de `input` enquanto se digita.
 * Aceita entrada parcial e nunca "explode" — 3 dígitos viram "(83) 9", não erro.
 *
 * Celular (11 dígitos): (83) 9 9999-9999 — o 9 separado, como você pediu.
 * Fixo    (10 dígitos): (83) 3333-4444.
 *
 * O ramo entre os dois formatos decide pela QUANTIDADE de dígitos depois do DDD:
 * 9 dígitos é celular, 8 é fixo. Enquanto se digita, ele começa como fixo e vira
 * celular ao cruzar o 9º dígito — um pequeno salto visual, aceitável porque a
 * alternativa (adivinhar cedo) erraria metade das vezes.
 */
export function formatarTelefone(bruto: string): string {
  const d = soDigitos(bruto).slice(0, 11) // 11 é o teto: DDD + 9 dígitos

  if (d.length === 0) return ''

  const ddd = d.slice(0, 2)
  if (d.length <= 2) return `(${ddd}`

  const resto = d.slice(2)

  // Fixo: 8 dígitos → XXXX-XXXX.
  if (resto.length <= 8) {
    const meio = resto.slice(0, 4)
    const fim = resto.slice(4, 8)
    return fim ? `(${ddd}) ${meio}-${fim}` : `(${ddd}) ${meio}`
  }

  // Celular: 9 dígitos → 9 XXXX-XXXX.
  const nono = resto.slice(0, 1)
  const meio = resto.slice(1, 5)
  const fim = resto.slice(5, 9)
  return `(${ddd}) ${nono} ${meio}-${fim}`
}
