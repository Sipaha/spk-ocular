# SPK Ocular 1.4.0

## Búsqueda y herramientas rápidas

- Ctrl+K recupera consultas QWERTY en inglés escritas con una distribución rusa o ucraniana compatible cuando la consulta original no tiene resultados. Se conservan el texto, los ámbitos y la identidad del recurso; un aviso traducido muestra la consulta recuperada.
- Al abrir un objeto desde la búsqueda, su fila seleccionada recibe el foco. Los logs, el terminal y los archivos de un Pod concreto usan el contenedor predeterminado en ejecución o, en su defecto, el primero en ejecución. Los logs siguen disponibles para Pods finalizados. Entre varias réplicas se mantiene la selección explícita del Pod; el clic derecho abre la configuración completa.
- Abrir o volver a abrir logs enfoca su área. Follow muestra End en la ayuda y en la descripción de la tecla; los campos de texto conservan el comportamiento habitual de End.

## Interfaz, fiabilidad y rendimiento

- Los diálogos modales cubren las barras de desplazamiento y los separadores del fondo. El indicador de foco incluye la cabecera y las filas; los menús permanecen sobre el panel inferior y las descripciones junto a sus botones.
- Los diálogos de lectura y configuración comparten el comportamiento de foco, Tab y cierre. Se conserva la protección de cambios sin guardar.
- El historial del agente descarta respuestas antiguas tras cambiar filtros, conserva destinos históricos y formatea fechas según el idioma. Las relaciones, revisiones de Deployment y recursos Helm usan tablas compactas y alineadas.
- Las solicitudes HTTP de la interfaz tienen límites de tamaño y tiempo de lectura. La comprobación de identidad de sesión y la cancelación de consultas de inventario en espera impiden lecturas antiguas en sesiones sustitutas. Permisos, ámbitos y límites de proveedores no cambian.
- La búsqueda regex en logs procesa lotes incrementales sin una segunda copia del historial en el worker. Las proyecciones Compose usan índices locales; las filas sin cambios y las actualizaciones solo de estado evitan trabajo redundante.
- Se mantienen los ocho idiomas, el ejecutable unificado de escritorio/navegador y la verificación obligatoria de las seis variantes de plataforma.
