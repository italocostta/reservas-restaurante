import { minutosNoDia } from '@/lib/tempo'
import type { DateOnly, Reservation, TableAvailability, Timestamp } from '@/types/api'

/** Uma reserva desenhada na linha de UMA mesa, já em minutos do dia. */
export interface Bloco {
  reserva: Reservation
  inicioMin: number
  fimMin: number
  /** A reserva ocupa mais de uma mesa: é uma COMBINAÇÃO (Fase 3a). */
  combinada: boolean
}

/** Uma linha da grade: a mesa e o que está em cima dela. */
export interface LinhaGrade {
  mesa: TableAvailability
  blocos: Bloco[]
}

/**
 * Cruza a grade de disponibilidade (quem está LIVRE) com as reservas do dia (quem
 * está LÁ). São duas requisições e um cruzamento no cliente.
 *
 * Este cruzamento NÃO é o sweep de volta. O sweep — subtrair janelas ocupadas do
 * expediente, recortando reservas que passam do fechamento — continua inteiro no
 * Go (`janelasLivres`, a única lógica de domínio real do projeto). O que acontece
 * aqui é só espalhar cada reserva pelas mesas que ela ocupa, e isso é informação
 * que a própria reserva carrega (`table_ids`). É um `flatMap`, não um algoritmo.
 *
 * Uma reserva COMBINADA aparece em cada uma das mesas que ela ocupa — é o que o
 * staff precisa ver: as duas mesas estão presas ao mesmo grupo.
 */
export function montarGrade(
  disponibilidade: TableAvailability[],
  reservas: Reservation[],
  tz: string,
  dia: DateOnly,
): LinhaGrade[] {
  return disponibilidade.map((mesa) => ({
    mesa,
    blocos: reservas
      .filter((r) => r.table_ids.includes(mesa.table_id))
      .map((reserva) => ({
        reserva,
        inicioMin: minutosNoDia(reserva.starts_at, tz, dia),
        fimMin: minutosNoDia(reserva.ends_at, tz, dia),
        combinada: reserva.table_ids.length > 1,
      }))
      .sort((a, b) => a.inicioMin - b.inicioMin),
  }))
}

/**
 * A mesa está livre no intervalo [inicio, fim)?
 *
 * ESTAS TRÊS LINHAS SÃO O MOTIVO DE O GET /availability EXISTIR.
 *
 * A alternativa — sem aquele endpoint — era o frontend refazer o sweep em
 * JavaScript, ou disparar uma requisição por mesa a cada vez que o staff mexesse
 * no horário. Com as janelas livres já calculadas pelo Go, "quem está livre das
 * 20h às 22h?" vira uma checagem de CONTENÇÃO: existe alguma janela livre que
 * cabe o intervalo inteiro dentro dela?
 *
 * Comparação por getTime() e não lexical: as strings da API vêm todas com o mesmo
 * offset, então comparar texto até funcionaria — hoje. Bastaria o backend passar a
 * serializar em UTC ("Z") para a comparação lexical continuar compilando e começar
 * a mentir.
 */
export function livreEm(mesa: TableAvailability, inicio: Timestamp, fim: Timestamp): boolean {
  const i = new Date(inicio).getTime()
  const f = new Date(fim).getTime()

  return mesa.free_windows.some(
    (w) => new Date(w.starts_at).getTime() <= i && new Date(w.ends_at).getTime() >= f,
  )
}

/**
 * Os limites da régua do dia.
 *
 * Começa na abertura, mas NÃO termina necessariamente no fechamento: a validação 8
 * da spec permite `ends_at` ultrapassar o `SERVICE_END` — é a última mesa do dia,
 * que senta 22h30 e sai 00h30. Se a régua parasse às 23h, essa reserva seria
 * desenhada saindo pela borda direita, ou pior, recortada como se terminasse no
 * fechamento.
 *
 * A régua ESTICA para caber a reserva mais longa. O expediente continua marcado —
 * é a área sombreada — mas ele não é a fronteira do desenho.
 */
export function limitesDaRegua(
  aberturaMin: number,
  fechamentoMin: number,
  linhas: LinhaGrade[],
): { inicio: number; fim: number } {
  const fins = linhas.flatMap((l) => l.blocos.map((b) => b.fimMin))
  const inicios = linhas.flatMap((l) => l.blocos.map((b) => b.inicioMin))

  return {
    // Math.min com os inícios cobre o caso simétrico (uma reserva que começasse
    // antes da abertura). Hoje a validação 8 impede isso — mas o desenho não deve
    // depender de uma regra de negócio para não vazar pela borda.
    inicio: Math.min(aberturaMin, ...inicios),
    fim: Math.max(fechamentoMin, ...fins),
  }
}
