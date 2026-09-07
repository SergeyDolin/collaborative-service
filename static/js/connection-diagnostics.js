(function() {
    const processNames = {disabled:'Расчёт на сервере отключён оператором',stopped:'Расчёт остановлен',waiting:'Запуск ожидается или не удался — проверьте состояние сервиса',running:'Процесс расчёта запущен',exited:'Процесс расчёта завершился; ожидается перезапуск'};
    window.renderConnectionDiagnostics = function(session) {
        const d = session?.diagnostics;
        const host = document.createElement('div');
        host.className = 'workflow-panel';
        const title = document.createElement('h3'); title.textContent = 'Состояние подключения'; host.append(title);
        const items = [d ? processNames[d.processState] || 'Состояние расчёта неизвестно' : 'Диагностика сервера недоступна',
          'Входной поток: нет отдельной телеметрии приёмника',
          'Поправки: нет отдельной телеметрии потока',
          d?.solutionState === 'fresh' ? 'Решение: получено недавно' : d?.solutionState === 'stale' ? 'Решение: устарело, не используйте как текущую позицию' : 'Решение: ещё не получено'];
        if (d?.lastSolutionAt) items.push('Последнее обновление решения: ' + new Date(d.lastSolutionAt).toLocaleString('ru-RU'));
        const list = document.createElement('ul'); list.className = 'workflow-checks';
        items.forEach(text => {const li = document.createElement('li'); li.textContent = text; list.append(li);}); host.append(list);
        const p = document.createElement('p'); p.className='workflow-note';
        p.textContent = d?.processState === 'disabled' ? 'Параметры подключения сохранены. Для запуска расчёта обратитесь к оператору сервиса.' : 'Если нет решения: проверьте передачу данных устройством, адрес и порт, а для NTRIP — учётные данные и mountpoint. Запущенный процесс не подтверждает поступление данных.';
        host.append(p); return host.outerHTML;
    };
})();
