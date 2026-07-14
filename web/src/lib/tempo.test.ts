import { describe, expect, it } from 'vitest'

import {
  diaPorExtenso,
  hhmmParaMinutos,
  horaLocal,
  instanteDe,
  minutosNoDia,
  minutosParaHHMM,
} from './tempo'

const SP = 'America/Sao_Paulo'

/**
 * O único módulo do frontend com teste, e isso é decisão, não preguiça.
 *
 * Testar componente Vue aqui seria testar que o Vue funciona. Este módulo é
 * diferente: ele é a única coisa do cliente que pode estar errada SEM A TELA
 * DENUNCIAR — uma reserva três horas deslocada aparece deslocada na tela também,
 * de forma consistente, e a interface concorda com ela mesma enquanto discorda do
 * banco.
 *
 * É o mesmo critério que a spec usa para justificar o teste do janelasLivres: a
 * única lógica que não dá para conferir de olho.
 */
describe('instanteDe — o texto do staff vira um instante', () => {
  it('20:00 em São Paulo é 23:00 UTC', () => {
    // SP está em GMT-3. As 20h de parede da mesa são 23h no instante absoluto.
    expect(instanteDe('2026-08-01', '20:00', SP)).toBe('2026-08-01T23:00:00.000Z')
  })

  it('NÃO usa o fuso do navegador', () => {
    // Este é O teste. Se alguém "simplificar" o instanteDe para
    // `new Date(dia + 'T' + hhmm)`, ele passa a depender do relógio da máquina — e
    // este caso quebra em qualquer runner que não esteja em SP.
    //
    // A asserção é contra o valor ABSOLUTO em UTC, não contra um round-trip: um
    // round-trip pelo próprio módulo passaria feliz mesmo se ele estivesse
    // deslocando tudo em três horas, porque o erro se cancelaria.
    expect(instanteDe('2026-01-15', '18:00', SP)).toBe('2026-01-15T21:00:00.000Z')
    expect(instanteDe('2026-12-25', '23:00', SP)).toBe('2026-12-26T02:00:00.000Z')
  })

  it('vira o dia quando a hora local empurra o instante para o dia seguinte', () => {
    // 23h de SP é 02h UTC do dia SEGUINTE. Se o instanteDe montasse a string na
    // mão em vez de deixar o Date fazer a aritmética, isto sairia como dia 25.
    expect(instanteDe('2026-12-25', '23:00', SP)).toBe('2026-12-26T02:00:00.000Z')
  })

  it('respeita o offset de um fuso COM horário de verão', () => {
    // Nova York muda de offset no ano: -05:00 no inverno, -04:00 no verão. Se o
    // offsetEm fosse uma constante cravada, um dos dois casos abaixo falharia.
    //
    // O restaurante é em SP e o Brasil não tem DST hoje. Mas "hoje" é uma aposta,
    // e SERVICE_TZ é configurável — o dia em que ela apontar para um fuso com DST,
    // ou em que o Brasil trouxer o horário de verão de volta, este código não pode
    // ser o que quebra.
    const NY = 'America/New_York'
    expect(instanteDe('2026-01-15', '12:00', NY)).toBe('2026-01-15T17:00:00.000Z') // EST, -5
    expect(instanteDe('2026-07-15', '12:00', NY)).toBe('2026-07-15T16:00:00.000Z') // EDT, -4
  })
})

describe('horaLocal — o instante da API vira hora de parede', () => {
  it('devolve a hora do RESTAURANTE, não a do navegador', () => {
    expect(horaLocal('2026-08-01T23:00:00Z', SP)).toBe('20:00')
  })

  it('fecha o ciclo com o instanteDe', () => {
    const iso = instanteDe('2026-08-01', '19:30', SP)
    expect(horaLocal(iso, SP)).toBe('19:30')
  })
})

describe('minutosNoDia — posiciona a barra na régua', () => {
  it('conta a partir da meia-noite do restaurante', () => {
    const inicio = instanteDe('2026-08-01', '19:00', SP)
    expect(minutosNoDia(inicio, SP, '2026-08-01')).toBe(19 * 60)
  })

  it('PASSA de 1440 quando a reserva atravessa a meia-noite', () => {
    // A validação 8 da spec permite ends_at ultrapassar o fechamento: é a última
    // mesa do dia, que senta 22h30 e sai 00h30. A barra dela precisa continuar
    // crescendo para a DIREITA na régua.
    //
    // Se minutosNoDia usasse getHours() em vez de aritmética de instantes, isto
    // devolveria 30 — e a barra voltaria para o começo do dia, atravessando a tela
    // ao contrário.
    const fim = instanteDe('2026-08-02', '00:30', SP)
    expect(minutosNoDia(fim, SP, '2026-08-01')).toBe(24 * 60 + 30)
  })
})

describe('diaPorExtenso', () => {
  it('não escorrega de dia por causa do fuso', () => {
    // O motivo de o diaPorExtenso formatar o MEIO-DIA e não a meia-noite: à
    // meia-noite, um deslocamento de uma hora muda o DIA da semana inteiro.
    expect(diaPorExtenso('2026-08-01', SP)).toContain('01')
    expect(diaPorExtenso('2026-08-01', SP)).toContain('sáb')
  })
})

describe('hhmm ↔ minutos', () => {
  it('vai e volta', () => {
    expect(hhmmParaMinutos('18:30')).toBe(1110)
    expect(minutosParaHHMM(1110)).toBe('18:30')
    expect(minutosParaHHMM(hhmmParaMinutos('00:00'))).toBe('00:00')
    expect(minutosParaHHMM(hhmmParaMinutos('23:59'))).toBe('23:59')
  })
})
