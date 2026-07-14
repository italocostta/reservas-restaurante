import { describe, expect, it } from 'vitest'

import { limitesDaRegua, livreEm, maiorCapacidade, montarGrade, sugerirCombinacao } from './grade'
import { instanteDe } from './tempo'
import type { Reservation, TableAvailability } from '@/types/api'

const SP = 'America/Sao_Paulo'
const DIA = '2026-08-01'

const em = (hhmm: string) => instanteDe(DIA, hhmm, SP)
const emAmanha = (hhmm: string) => instanteDe('2026-08-02', hhmm, SP)

function mesa(id: string, nome: string, capacidade: number, livres: [string, string][]): TableAvailability {
  return {
    table_id: id,
    table_name: nome,
    capacity: capacidade,
    free_windows: livres.map(([de, ate]) => ({ starts_at: em(de), ends_at: em(ate) })),
  }
}

function reserva(id: string, mesas: string[], de: string, ate: string, pessoas = 4): Reservation {
  return {
    id,
    table_ids: mesas,
    customer_name: 'Maria Silva',
    customer_phone: '11999998888',
    party_size: pessoas,
    starts_at: em(de),
    ends_at: ate.startsWith('24') || ate < de ? emAmanha(ate) : em(ate),
    status: 'confirmed',
    created_at: em('12:00'),
  }
}

describe('livreEm — a contenção que substitui o sweep no cliente', () => {
  // A mesa está livre das 18h às 20h e das 22h às 23h (ocupada 20h–22h).
  const m = mesa('a', 'Mesa 04', 4, [
    ['18:00', '20:00'],
    ['22:00', '23:00'],
  ])

  it('cabe inteiro dentro de uma janela', () => {
    expect(livreEm(m, em('18:30'), em('19:30'))).toBe(true)
  })

  it('encosta exatamente nos limites da janela', () => {
    // [) do banco: uma reserva 18h–20h cabe na janela 18h–20h. O fim de uma libera
    // o instante em que a próxima começa (seção 2 da spec).
    expect(livreEm(m, em('18:00'), em('20:00'))).toBe(true)
  })

  it('NÃO cabe se atravessa a janela ocupada', () => {
    expect(livreEm(m, em('19:00'), em('21:00'))).toBe(false)
  })

  it('NÃO cabe emendando DUAS janelas livres com um buraco no meio', () => {
    // Este é o caso que separa "contenção" de "sobreposição". Das 18h às 23h existe
    // interseção com as duas janelas livres — mas a mesa está OCUPADA das 20h às
    // 22h. Um `.some(w => overlap(w, pedido))` devolveria `true` e ofereceria uma
    // mesa que está com gente sentada.
    expect(livreEm(m, em('18:00'), em('23:00'))).toBe(false)
  })

  it('mesa sem nenhuma janela livre não cabe nada', () => {
    expect(livreEm(mesa('b', 'Mesa 05', 4, []), em('19:00'), em('20:00'))).toBe(false)
  })
})

describe('montarGrade — espalha a reserva pelas mesas que ela ocupa', () => {
  const mesas = [
    mesa('a', 'Mesa 01', 4, []),
    mesa('b', 'Mesa 02', 6, []),
    mesa('c', 'Mesa 03', 2, []),
  ]

  it('uma COMBINAÇÃO aparece nas DUAS linhas', () => {
    // É o que o staff precisa enxergar: as duas mesas estão presas ao mesmo grupo.
    // Se a combinação aparecesse só na primeira mesa, a segunda pareceria livre.
    const grade = montarGrade(mesas, [reserva('r1', ['a', 'b'], '20:00', '22:00', 10)], SP, DIA)

    expect(grade[0].blocos).toHaveLength(1)
    expect(grade[1].blocos).toHaveLength(1)
    expect(grade[2].blocos).toHaveLength(0)

    expect(grade[0].blocos[0].combinada).toBe(true)
    expect(grade[0].blocos[0].reserva.id).toBe('r1')
  })

  it('uma reserva de mesa única não é combinação', () => {
    const grade = montarGrade(mesas, [reserva('r1', ['a'], '20:00', '22:00')], SP, DIA)
    expect(grade[0].blocos[0].combinada).toBe(false)
  })

  it('converte os instantes em minutos do dia', () => {
    const grade = montarGrade(mesas, [reserva('r1', ['a'], '20:00', '22:00')], SP, DIA)
    expect(grade[0].blocos[0].inicioMin).toBe(20 * 60)
    expect(grade[0].blocos[0].fimMin).toBe(22 * 60)
  })

  it('ordena os blocos por início', () => {
    const grade = montarGrade(
      mesas,
      [reserva('tarde', ['a'], '21:00', '23:00'), reserva('cedo', ['a'], '18:00', '20:00')],
      SP,
      DIA,
    )
    expect(grade[0].blocos.map((b) => b.reserva.id)).toEqual(['cedo', 'tarde'])
  })
})

describe('maiorCapacidade — o teto do modo automático', () => {
  it('devolve a maior capacidade individual, não a soma', () => {
    const salao = [mesa('a', 'M1', 2, []), mesa('b', 'M2', 8, []), mesa('c', 'M3', 4, [])]
    expect(maiorCapacidade(salao)).toBe(8)
  })

  it('salão vazio é 0 — e o dialog usa isso para NÃO bloquear enquanto carrega', () => {
    // maiorMesa === 0 desliga o bloqueio: sem dados ainda, não dá para declarar
    // nada impossível. Se isto devolvesse -Infinity ou NaN, a comparação
    // `pessoas > maiorMesa` mentiria.
    expect(maiorCapacidade([])).toBe(0)
  })
})

describe('sugerirCombinacao — o ponto de partida editável', () => {
  const salao = [
    mesa('p', 'Pequena', 2, []),
    mesa('g', 'Grande', 8, []),
    mesa('m', 'Media', 6, []),
    mesa('q', 'Quatro', 4, []),
  ]

  it('pega as MAIORES primeiro, para juntar o mínimo de mesas', () => {
    // 17 pessoas: 8 + 6 + 4 = 18 cobre com três mesas. Começar pelas pequenas
    // exigiria mais mesas para o mesmo grupo.
    const ids = sugerirCombinacao(salao, 17)
    expect(ids).toEqual(['g', 'm', 'q'])
  })

  it('para assim que cobre o grupo', () => {
    // 7 pessoas: a Grande (8) sozinha já basta. Não deve marcar mais nada.
    expect(sugerirCombinacao(salao, 7)).toEqual(['g'])
  })

  it('grupo que excede o salão inteiro devolve TODAS as mesas', () => {
    // 40 pessoas num salão de 20 lugares: marca tudo. Não é solução — é o máximo
    // que dá para oferecer, e o servidor recusará com a soma insuficiente. A UI
    // não finge que resolveu.
    const ids = sugerirCombinacao(salao, 40)
    expect(ids).toHaveLength(4)
  })
})

describe('limitesDaRegua', () => {
  const mesas = [mesa('a', 'Mesa 01', 4, [])]

  it('sem reservas, a régua é o expediente', () => {
    const grade = montarGrade(mesas, [], SP, DIA)
    expect(limitesDaRegua(18 * 60, 23 * 60, grade)).toEqual({ inicio: 1080, fim: 1380 })
  })

  it('ESTICA para caber a última mesa da noite, que passa do fechamento', () => {
    // Validação 8 da spec: ends_at PODE ultrapassar o SERVICE_END. Uma mesa que
    // senta 22h30 e sai 00h30 é legítima.
    //
    // Sem o esticão, a barra dela seria desenhada saindo pela borda direita da tela
    // — ou, pior, recortada como se a reserva terminasse às 23h, mentindo sobre a
    // ocupação real da mesa.
    const grade = montarGrade(mesas, [reserva('ultima', ['a'], '22:30', '00:30')], SP, DIA)
    const regua = limitesDaRegua(18 * 60, 23 * 60, grade)

    expect(regua.fim).toBe(24 * 60 + 30) // 00h30 do dia seguinte, na régua deste dia
    expect(regua.fim).toBeGreaterThan(23 * 60)
  })
})
