<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'

import { mensagemLegivel } from '@/api/errors'
import { useExpedienteStore } from '@/stores/expediente'

const expediente = useExpedienteStore()

// Rascunho local do formulário de horário — só é enviado ao salvar, para editar
// não disparar uma requisição a cada tecla.
const start = ref('18:00')
const end = ref('23:00')
const dias = ref<number[]>([])
const salvandoHoras = ref(false)
const erroHoras = ref<string | null>(null)
const salvouHoras = ref(false)

// Nova exceção.
const novaData = ref('')
const novaAberta = ref(false)
const novaNota = ref('')
const salvandoExcecao = ref(false)
const erroExcecao = ref<string | null>(null)

const NOMES_DIAS = ['Dom', 'Seg', 'Ter', 'Qua', 'Qui', 'Sex', 'Sáb']

onMounted(() => expediente.carregar())

// Quando o expediente carrega (ou recarrega), copia para o rascunho local.
watch(
  () => expediente.horas,
  (h) => {
    if (!h) return
    start.value = h.start
    end.value = h.end
    dias.value = [...h.open_weekdays]
  },
  { immediate: true },
)

function alternarDia(d: number) {
  const i = dias.value.indexOf(d)
  if (i === -1) dias.value.push(d)
  else dias.value.splice(i, 1)
}

async function salvarHoras() {
  if (dias.value.length === 0) {
    erroHoras.value = 'Marque ao menos um dia da semana.'
    return
  }
  salvandoHoras.value = true
  erroHoras.value = null
  try {
    await expediente.salvarHoras({
      start: start.value,
      end: end.value,
      tz: expediente.tz,
      open_weekdays: [...dias.value].sort((a, b) => a - b),
    })
    salvouHoras.value = true
    setTimeout(() => (salvouHoras.value = false), 1600)
  } catch (e) {
    erroHoras.value = mensagemLegivel(e)
  } finally {
    salvandoHoras.value = false
  }
}

async function adicionarExcecao() {
  if (!novaData.value) {
    erroExcecao.value = 'Escolha uma data.'
    return
  }
  salvandoExcecao.value = true
  erroExcecao.value = null
  try {
    await expediente.salvarExcecao({
      day: novaData.value,
      is_open: novaAberta.value,
      note: novaNota.value.trim(),
    })
    novaData.value = ''
    novaNota.value = ''
    novaAberta.value = false
  } catch (e) {
    erroExcecao.value = mensagemLegivel(e)
  } finally {
    salvandoExcecao.value = false
  }
}
</script>

<template>
  <div class="max-w-2xl">
    <h1 class="font-display mb-8 text-2xl font-bold tracking-wide uppercase">Restaurante</h1>

    <p v-if="expediente.erro" class="border-blood-500/40 bg-blood-500/10 text-blood-300 mb-6 border-l-2 px-4 py-3 text-sm">
      {{ expediente.erro }}
    </p>

    <!-- HORÁRIO -->
    <section class="border-ink-800 mb-8 border p-6">
      <h2 class="rotulo mb-4">Expediente</h2>

      <div class="mb-6 grid grid-cols-2 gap-4">
        <div>
          <label for="start" class="rotulo mb-2 block">Abre</label>
          <input
            id="start"
            v-model="start"
            type="time"
            step="1800"
            class="dado bg-ink-950 border-ink-700 focus:border-ember-500 w-full border px-3 py-2.5 text-lg font-medium outline-none"
          />
        </div>
        <div>
          <label for="end" class="rotulo mb-2 block">Fecha</label>
          <input
            id="end"
            v-model="end"
            type="time"
            step="1800"
            class="dado bg-ink-950 border-ink-700 focus:border-ember-500 w-full border px-3 py-2.5 text-lg font-medium outline-none"
          />
        </div>
      </div>

      <span class="rotulo mb-2 block">Dias de funcionamento</span>
      <div class="mb-5 flex gap-1.5">
        <button
          v-for="(nome, d) in NOMES_DIAS"
          :key="d"
          type="button"
          class="font-display flex-1 border py-2.5 text-xs font-bold tracking-wide uppercase transition-colors"
          :class="
            dias.includes(d)
              ? 'bg-ember-500 border-ember-500 text-ink-950'
              : 'border-ink-700 text-ink-500 hover:text-ink-200'
          "
          @click="alternarDia(d)"
        >
          {{ nome }}
        </button>
      </div>

      <p v-if="erroHoras" class="border-blood-500/40 bg-blood-500/10 text-blood-300 mb-3 border-l-2 px-3 py-2 text-sm">
        {{ erroHoras }}
      </p>

      <div class="flex items-center gap-3">
        <button
          type="button"
          :disabled="salvandoHoras"
          class="font-display bg-ember-500 text-ink-950 hover:bg-ember-400 px-5 py-2 text-sm font-bold tracking-wide uppercase transition-colors disabled:opacity-50"
          @click="salvarHoras"
        >
          {{ salvandoHoras ? 'Salvando…' : 'Salvar expediente' }}
        </button>
        <span v-if="salvouHoras" class="rotulo text-sage-500">✓ salvo</span>
      </div>
    </section>

    <!-- EXCEÇÕES -->
    <section class="border-ink-800 border p-6">
      <h2 class="rotulo mb-1">Exceções por data</h2>
      <p class="text-ink-500 mb-5 text-xs">
        Feche numa data que normalmente abre (feriado), ou abra numa que normalmente
        fecha.
      </p>

      <ul v-if="expediente.horas?.exceptions.length" class="border-ink-800 mb-5 divide-y divide-ink-800 border">
        <li
          v-for="ex in expediente.horas.exceptions"
          :key="ex.day"
          class="flex items-center gap-3 px-3 py-2.5"
        >
          <span class="dado text-ink-200 text-sm">{{ ex.day }}</span>
          <span
            class="rotulo"
            :class="ex.is_open ? 'text-sage-500' : 'text-blood-300'"
          >
            {{ ex.is_open ? 'Aberto' : 'Fechado' }}
          </span>
          <span class="text-ink-500 flex-1 truncate text-sm">{{ ex.note }}</span>
          <button
            type="button"
            class="font-display text-ink-500 hover:text-blood-300 text-xs font-semibold tracking-wide uppercase transition-colors"
            @click="expediente.removerExcecao(ex.day)"
          >
            Remover
          </button>
        </li>
      </ul>
      <p v-else class="text-ink-600 mb-5 text-sm">Nenhuma exceção cadastrada.</p>

      <div class="grid grid-cols-[10rem_1fr_auto] items-end gap-3">
        <div>
          <label for="exday" class="rotulo mb-2 block">Data</label>
          <input
            id="exday"
            v-model="novaData"
            type="date"
            class="dado bg-ink-950 border-ink-700 focus:border-ember-500 w-full border px-3 py-2 text-sm outline-none"
          />
        </div>
        <div>
          <label for="exnote" class="rotulo mb-2 block">Nota</label>
          <input
            id="exnote"
            v-model="novaNota"
            type="text"
            placeholder="Natal"
            class="bg-ink-950 border-ink-700 placeholder:text-ink-600 focus:border-ember-500 w-full border px-3 py-2 text-sm outline-none"
          />
        </div>
        <label class="flex cursor-pointer items-center gap-2 py-2">
          <input v-model="novaAberta" type="checkbox" class="accent-ember-500 h-4 w-4" />
          <span class="rotulo">Abre</span>
        </label>
      </div>

      <p v-if="erroExcecao" class="border-blood-500/40 bg-blood-500/10 text-blood-300 mt-3 border-l-2 px-3 py-2 text-sm">
        {{ erroExcecao }}
      </p>

      <button
        type="button"
        :disabled="salvandoExcecao"
        class="font-display border-ember-500 text-ember-400 hover:bg-ember-500 hover:text-ink-950 mt-4 border px-4 py-2 text-sm font-bold tracking-wide uppercase transition-colors disabled:opacity-50"
        @click="adicionarExcecao"
      >
        + Adicionar exceção
      </button>
    </section>
  </div>
</template>
