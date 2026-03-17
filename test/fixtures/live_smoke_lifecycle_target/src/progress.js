function percent(complete, total) {
  if (total <= 0) {
    return 0;
  }

  return Math.round((complete / total) * 100);
}

module.exports = {
  percent,
};
