# SPK Ocular 1.9.0

- Recursos, Helm, Graph, Timeline, Security y RBAC comparten barras y controles coherentes de carga, progreso, cancelación y reintento sin borrar el contenido. Helm aparece a la izquierda después de Storage.
- Las propiedades de Pod separan contenedores normales, init y debug, conservan etiquetas Sidecar y ajustan nombres e imágenes largos.
- Las fechas usan hora local, formato de 24 horas y dd.mm.yyyy en todos los idiomas; al pasar el cursor se muestra el valor ISO original.
- Escale Deployments desde sus propiedades. La revisión muestra el nombre de la conexión; los botones menos/más ajustan réplicas dentro del rango permitido antes de revisar.
- Elegir una referencia de comparación ya no desplaza el área de trabajo. Las diferencias incluyen contexto y paginación limitada.
- Los nuevos permisos container-exec abarcan el namespace/proyecto elegido sin selector de tipos. Se conservan permisos restringidos y confirmaciones existentes; las casillas activas y deshabilitadas se distinguen mejor.
- Los logs identifican conexión y recurso, usan todo el periodo y sitúan el filtro antes de la búsqueda local. El backend explora progresivamente un historial reciente limitado; muestra coincidencias mientras carga y captura nuevas líneas en paralelo. Los límites y resultados incompletos son explícitos.
- Downloads se abre automáticamente y guarda en la carpeta de descargas del sistema sin preguntar. El clic derecho en la descarga de logs permite elegir destino. Los nombres repetidos reciben un sufijo; se pueden cancelar tareas, incluidas las de ventanas de logs separadas.
- Pulse una fila de descarga completada para abrirla. Abrir el archivo o mostrar su carpeta correctamente cierra el panel. La posición, la altura de filas y el pie de logs permanecen estables al cambiar el progreso.
- El progreso de conexión ya no desplaza el icono y el título. YAML aparece sin un destello de texto sin formato, precarga el editor al mostrar intención de abrirlo y reutiliza su instancia al cambiar de recurso; se mantiene la protección de borradores.
