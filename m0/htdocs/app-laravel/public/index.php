<?php
// M0 dummy Laravel front controller - lives in public/, like real Laravel.
if (isset($_GET['check'])) {
    header('Content-Type: text/plain');
    echo 'project=app-laravel php=' . PHP_VERSION . ' sapi=' . php_sapi_name() . ' front=public/index.php';
    exit;
}
header('Content-Type: text/html; charset=utf-8');
echo '<h1>M0 Laravel dummy</h1>';
echo '<p>Reached via <code>/app-laravel/</code> without <code>/public</code>.</p>';
echo '<p>PHP ' . htmlspecialchars(PHP_VERSION) . ' &middot; SAPI ' . htmlspecialchars(php_sapi_name()) . '</p>';
